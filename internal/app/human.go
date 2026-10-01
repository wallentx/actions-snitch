package app

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/wallentx/actions-snitch/internal/compat"
	"github.com/wallentx/actions-snitch/internal/config"
	"github.com/wallentx/actions-snitch/internal/llm"
	"github.com/wallentx/actions-snitch/internal/model"
	"github.com/wallentx/actions-snitch/internal/output"
	"github.com/wallentx/actions-snitch/internal/update"
)

// humanTranscript delays success messages until the prepared files are committed,
// while retaining the original per-finding presentation order.
type humanTranscript struct {
	bytes.Buffer
	segments []string
}

func (t *humanTranscript) mark() { t.segments = append(t.segments, t.String()); t.Reset() }

func renderUpdates(w io.Writer, t *humanTranscript, style output.Style, o Options, cfg config.AI, findings []model.Finding, applied []update.Applied, assessments map[string]model.Assessment) error {
	var b strings.Builder
	updates := map[string]update.Applied{}
	for _, a := range applied {
		updates[output.AssessmentKey(a.Group.ActionPath, a.Group.Current, a.Group.Latest)+"\x00"+a.File] = a
	}
	remediated := map[string]bool{}
	for i, f := range findings {
		b.WriteString(t.segments[i])
		path := strings.SplitN(f.Action, "@", 2)[0]
		key := output.AssessmentKey(path, f.Current, f.Latest)
		gate := compat.Gate(f.CompatibilityScore, f.VerifiedCreator, o.VerifiedOnly, o.Force, cfg.Enabled, cfg.Threshold)
		if o.VerifiedOnly && !f.VerifiedCreator {
			_, _ = writeHumanLine(&b, style, "      Skipping update of %s because it is not from a GitHub Marketplace verified creator. Run without -t to include it.\n", f.Repository)
			continue
		}
		assessment, assessed := assessments[key]
		if assessed {
			_, _ = writeHumanLine(&b, style, "      AI assessment: %s\n", llm.SafeSummary(assessment.Summary))
			if assessment.Decision != "allow" {
				_, _ = writeHumanLine(&b, style, "      Skipping update of %s to v%s after AI decision '%s'. Use -f to force.\n", f.Repository, f.Latest, assessment.Decision)
				continue
			}
		}
		fileKey := key + "\x00" + f.File
		if applied, ok := updates[fileKey]; ok {
			if assessed && len(assessment.Remediations) > 0 && !remediated[key] {
				_, _ = writeHumanLine(&b, style, "      Applied %d scoped AI remediation(s) for %s\n", len(assessment.Remediations), path)
				remediated[key] = true
			}
			_, _ = writeHumanLine(&b, style, "      Updated %s from %s to %s\n", path, f.Current, applied.Group.Target)
			delete(updates, fileKey)
		} else if assessed && !remediated[key] && len(assessment.Remediations) > 0 {
			_, _ = writeHumanLine(&b, style, "      Skipping update of %s because its AI remediation could not be safely validated and applied. Use -f to force.\n", f.Repository)
		} else if !gate.Allow && !assessed {
			if score, known := f.CompatibilityScore.(int); known {
				_, _ = writeHumanLine(&b, style, "      Skipping update of %s to v%s due to low compatibility score (%d%%). Enable AI analysis or use -f to force.\n", f.Repository, f.Latest, score)
			} else {
				_, _ = writeHumanLine(&b, style, "      Skipping update of %s to v%s because its compatibility score is unknown. Enable AI analysis or use -f to force.\n", f.Repository, f.Latest)
			}
		}
	}
	b.WriteString(t.String())
	_, err := io.WriteString(w, b.String())
	return err
}

func writeHumanLine(w io.Writer, style output.Style, format string, args ...any) (int, error) {
	text := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
	class := "warning"
	if strings.HasPrefix(text, "      Updated ") || strings.HasPrefix(text, "      Applied ") {
		class = "success"
	}
	if strings.HasPrefix(text, "      AI assessment:") {
		class = "info"
	}
	return fmt.Fprintln(w, style.Wrap(class, text))
}
