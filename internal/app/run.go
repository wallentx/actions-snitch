package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wallentx/actions-snitch/internal/cache"
	"github.com/wallentx/actions-snitch/internal/compat"
	"github.com/wallentx/actions-snitch/internal/config"
	gitops "github.com/wallentx/actions-snitch/internal/git"
	"github.com/wallentx/actions-snitch/internal/github"
	"github.com/wallentx/actions-snitch/internal/llm"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/output"
	"github.com/wallentx/actions-snitch/internal/update"
	"github.com/wallentx/actions-snitch/internal/workflow"
)

// Runtime supplies process boundaries; normal CLI calls use the zero-value defaults.
type Runtime struct {
	Dir         string
	Lookup      config.Lookup
	Input       io.Reader
	Output      io.Writer
	Error       io.Writer
	Runner      gitops.Runner
	Client      *github.Client
	Model       llm.Generator
	Catalog     func(context.Context, string) ([]llm.ModelInfo, error)
	Interactive bool
	style       output.Style
}

func Run(ctx context.Context, args []string, r Runtime) int {
	if r.Lookup == nil {
		r.Lookup = os.LookupEnv
	}
	if r.Input == nil {
		r.Input = os.Stdin
	}
	if r.Output == nil {
		r.Output = os.Stdout
	}
	if r.Error == nil {
		r.Error = os.Stderr
	}
	if r.Runner == nil {
		r.Runner = gitops.Commands{}
	}
	if r.Dir == "" {
		dir, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(r.Error, err)
			return 1
		}
		r.Dir = dir
	}
	terminalName, _ := r.Lookup("TERM")
	r.style = output.TerminalStyle(terminalName)
	checked := &checkedWriter{Writer: r.Output}
	r.Output = checked
	o, err := ParseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintln(r.Error, "Error:", err)
		return 1
	}
	if o.Help {
		if o.Diagnostic != "" {
			_, _ = fmt.Fprintln(r.Error, r.style.Wrap("error", o.Diagnostic))
		}
		if _, err := io.WriteString(r.Output, r.style.Usage(Usage)); err != nil {
			return 1
		}
		return 0
	}
	if o.Configure {
		err = configure(ctx, r)
	} else {
		err = execute(ctx, o, r)
	}
	if o.Configure && errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(r.Error, "Setup cancelled; no config was written.")
		return 130
	}
	if err == nil {
		err = checked.Err
	}
	if err != nil {
		_, _ = fmt.Fprintln(r.Error, "Error:", err)
		return 1
	}
	return 0
}

func execute(ctx context.Context, o Options, r Runtime) error {
	cfg, err := config.Load(config.Path(r.Lookup), r.Lookup)
	if err != nil {
		return err
	}
	store := &cache.Store{Dir: config.CachePath(r.Lookup)}
	ai := llm.Service{Model: r.Model, Config: cfg.AI, Cache: store}
	if cfg.AI.Enabled && o.Update && !o.Force && ai.Model == nil {
		provider, endpoint, err := llm.NewProvider(ctx, cfg.AI, r.Lookup)
		if err != nil {
			return err
		}
		defer func() { _ = llm.Close(provider) }()
		ai.Model, ai.Endpoint = provider, endpoint
	}
	client := r.Client
	if client == nil {
		host, _ := r.Lookup("GH_HOST")
		if host == "" {
			host = "github.com"
		}
		client = github.New(host, gitops.Token(ctx, r.Runner, r.Dir, host, r.Lookup), store)
	}
	gitOutput := r.Output
	if o.Format != "" {
		gitOutput = r.Error
	}
	git := gitops.Service{Dir: r.Dir, Runner: r.Runner, Output: gitOutput}
	var target gitops.Target
	if o.PR || o.Branch != "" {
		target, err = git.Prepare(ctx, o.PR, o.Branch)
		if err != nil {
			return err
		}
	}
	if o.PR && o.Branch == "" && r.Interactive {
		branch, err := promptBranch(ctx, r)
		if err != nil {
			return err
		}
		if branch != "" {
			prepared, err := git.Prepare(ctx, false, branch)
			if err != nil {
				return err
			}
			target.Branch = prepared.Branch
		}
	}
	quiet := o.Format == "" && o.Verbosity == 0 && !o.Update
	if quiet {
		_, _ = fmt.Fprintln(r.Output, r.style.Wrap("status", "Checking "+r.Dir))
	}
	var animated bytes.Buffer
	scanRuntime := r
	ci, _ := r.Lookup("CI")
	animate := quiet && ci != "true" && r.style.HideCursor != ""
	stop := func() {}
	if animate {
		colorTerm, _ := r.Lookup("COLORTERM")
		stop = output.StartSpinner(ctx, r.Output, r.style, strings.Contains(colorTerm, "truecolor") || strings.Contains(colorTerm, "24bit"))
		scanRuntime.Output = &animated
		scanRuntime.Error = &animated
	}
	defer stop()
	files, err := scan(ctx, scanRuntime, o)
	if err != nil {
		return err
	}
	transcript := &humanTranscript{}
	if o.Update && o.Format == "" {
		scanRuntime.Output = transcript
	}
	findings, err := resolve(ctx, scanRuntime, o, client, files)
	if animate {
		stop()
		if _, writeErr := r.Output.Write(animated.Bytes()); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return err
	}
	if quiet {
		_, _ = fmt.Fprintln(r.Output, r.style.Wrap("success", "Finished."))
	}
	assessments := map[string]model.Assessment{}
	var applied []update.Applied
	if o.Update {
		planningRuntime := r
		planningRuntime.Output = io.Discard
		groups := planGroups(ctx, planningRuntime, o, cfg.AI, client, ai, files, findings, assessments)
		if err := ctx.Err(); err != nil {
			return err
		}
		plan, err := update.Build(files, groups)
		if err != nil {
			return err
		}
		if writer, ok := r.Output.(*checkedWriter); ok && writer.Err != nil {
			return writer.Err
		}
		if err := plan.Commit(ctx, r.Dir); err != nil {
			return err
		}
		applied = plan.Applied
		if o.Format == "" {
			if err := renderUpdates(r.Output, transcript, r.style, o, cfg.AI, findings, applied, assessments); err != nil {
				return err
			}
			count := 0
			for _, a := range applied {
				count += a.Count
			}
			if count == 0 {
				if o.PR {
					_, _ = fmt.Fprintln(r.Output, r.style.Wrap("status", "No action updates were made; skipping commit, push, and PR creation."))
				} else {
					_, _ = fmt.Fprintln(r.Output, r.style.Wrap("status", "No action updates were made."))
				}
			} else if !o.PR {
				_, _ = fmt.Fprintln(r.Output, r.style.Wrap("success", fmt.Sprintf("Updated %d action reference(s).", count)))
			}
		}
	}
	if o.PR && len(applied) > 0 {
		paths := []string{}
		for _, a := range applied {
			paths = append(paths, a.File)
		}
		body := output.PRBody(ctx, client, applied, findings, assessments)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := git.Publish(ctx, target, paths, output.PRTitle(applied), body); err != nil {
			return err
		}
	}
	if o.Format != "" {
		name, repoURL := git.Identity(ctx)
		return output.Report(r.Output, o.Format, name, repoURL, findings)
	}
	return nil
}

func scan(ctx context.Context, r Runtime, o Options) ([]*workflow.File, error) {
	paths, warnings, err := workflow.DiscoverWithWarnings(r.Dir)
	for _, warning := range warnings {
		_, _ = fmt.Fprintln(r.Error, warning)
	}
	if err != nil {
		return nil, err
	}
	files := make([]*workflow.File, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := workflow.Read(r.Dir, path)
		if err != nil {
			if o.Verbosity > 0 {
				_, _ = fmt.Fprintf(r.Error, "Skipping %s: %v\n", path, err)
			}
			files = append(files, &workflow.File{Path: path, Problem: err})
			continue
		}
		files = append(files, file)

	}
	return files, nil
}

func resolve(ctx context.Context, r Runtime, o Options, client *github.Client, files []*workflow.File) ([]model.Finding, error) {
	var findings []model.Finding
	for _, file := range files {
		headerPrinted := false
		if o.Verbosity > 0 && o.Format == "" {
			_, _ = fmt.Fprintln(r.Output, r.style.Wrap("info", "Checking file: "+file.Path))
			if len(file.Usages) == 0 {
				_, _ = fmt.Fprintln(r.Output, r.style.Wrap("status", "  No actions found in "+file.Path))
			}
		}
		for _, u := range file.Usages {
			if o.Verbosity > 0 && o.Format == "" {
				_, _ = fmt.Fprintln(r.Output, r.style.Wrap("info", fmt.Sprintf("Found action at line %d: %s", u.Line, u.Action)))
				if strings.Count(u.ActionPath, "/") >= 3 && !strings.Contains(u.ActionPath, "docker://") {
					_, _ = fmt.Fprintln(r.Output, r.style.Wrap("status", "  🔒 Skipping internal action: "+u.ActionPath))
				}
				if u.Current == "main" || u.Current == "master" {
					_, _ = fmt.Fprintln(r.Output, r.style.Wrap("status", fmt.Sprintf("  🚫 Skipping %s targeting branch '%s'", u.Repository, u.Current)))
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			result := client.Resolve(ctx, u, o.Pin)
			for _, warning := range result.Warnings {
				_, _ = fmt.Fprintln(r.Error, r.style.Wrap("warning", warning))
			}
			if o.Verbosity > 0 {
				for _, message := range result.Debug {
					_, _ = fmt.Fprintln(r.Error, r.style.Wrap("error", message))
				}
				if o.Format == "" && result.Unavailable {
					_, _ = fmt.Fprintln(r.Error, r.style.Wrap("warning", "  Unable to determine latest version for "+u.Repository))
				}
				if o.Format == "" && result.Current {
					prefix := "  ✅ "
					if github.IsSHA(u.Current) {
						prefix = "  "
					}
					_, _ = fmt.Fprintln(r.Output, r.style.Wrap("success", fmt.Sprintf("%s%s is up-to-date (%s)", prefix, u.Repository, u.Current)))
				}
			}
			if result.Finding == nil {
				continue
			}
			f := *result.Finding
			current := f.Current
			if github.IsSHA(f.Current) {
				current = ""
				if f.CurrentTag != nil {
					current = strings.TrimPrefix(*f.CurrentTag, "v")
				}
			}
			if current != "" {
				f.CompatibilityScore = client.Score(ctx, f.Repository, current, f.Latest)
			}
			f.VerifiedCreator = client.Verified(ctx, f.Repository)
			findings = append(findings, f)
			if o.Format == "" {
				var display bytes.Buffer
				if err := output.HumanStyled(&display, []model.Finding{f}, o.Pin, r.style); err != nil {
					return nil, err
				}
				data := display.Bytes()
				if headerPrinted {
					data = data[bytes.IndexByte(data, '\n')+1:]
				}
				headerPrinted = true
				if _, err := r.Output.Write(data); err != nil {
					return nil, err
				}
				if transcript, ok := r.Output.(*humanTranscript); ok {
					transcript.mark()
				}
			}
		}
	}
	return findings, ctx.Err()
}

func planGroups(ctx context.Context, r Runtime, o Options, cfg config.AI, client *github.Client, ai llm.Service, files []*workflow.File, findings []model.Finding, assessments map[string]model.Assessment) []update.Group {
	var groups []update.Group
	seen := map[string]bool{}
	for _, f := range findings {
		if ctx.Err() != nil {
			break
		}
		path := strings.SplitN(f.Action, "@", 2)[0]
		key := output.AssessmentKey(path, f.Current, f.Latest)
		if seen[key] {
			continue
		}
		seen[key] = true
		gate := compat.Gate(f.CompatibilityScore, f.VerifiedCreator, o.VerifiedOnly, o.Force, cfg.Enabled, cfg.Threshold)
		group := update.Group{ActionPath: path, Current: f.Current, Latest: f.Latest, Target: f.UpdateRef}
		if gate.Assess {
			evidence, err := llm.Evidence(ctx, client, files, f, cfg.IssueSearch)
			assessment := llm.Review("Evidence collection failed.")
			if err == nil {
				assessment = ai.Assess(ctx, evidence)
			}
			assessments[key] = assessment
			if o.Format == "" {
				_, _ = fmt.Fprintf(r.Output, "      AI assessment: %s\n", llm.SafeSummary(assessment.Summary))
			}
			gate.Allow = assessment.Decision == "allow"
			group.Remediations = assessment.Remediations
		}
		if !gate.Allow {
			if o.Format == "" {
				_, _ = fmt.Fprintf(r.Output, "      Skipping update of %s: %s.\n", f.Repository, gate.Reason)
			}
			continue
		}
		if _, err := update.Build(files, []update.Group{group}); err != nil {
			_, _ = fmt.Fprintf(r.Error, "Skipping update of %s: %v\n", f.Repository, err)
			continue
		}
		groups = append(groups, group)
	}
	return groups
}

type checkedWriter struct {
	io.Writer
	Err error
}

func (w *checkedWriter) Write(p []byte) (int, error) {
	if w.Err != nil {
		return 0, w.Err
	}
	n, err := w.Writer.Write(p)
	w.Err = err
	return n, err
}
