// Package app composes the command's independently testable subsystems.
package app

import "fmt"

type Options struct {
	Configure    bool
	Update       bool
	Pin          bool
	Force        bool
	PR           bool
	Branch       string
	Format       string
	VerifiedOnly bool
	Verbosity    int
	Help         bool
	Diagnostic   string
}

const Usage = `Usage: actions-snitch [-c] [-u] [-s] [-f] [-p] [-b branch] [-o format] [-t] [-v] [-h]
Options:
  -c    Interactively create the config file, then exit (use alone)
  -u    Update outdated actions in-place
  -s    Pin updates to full commit SHAs (scan without -u to preview)
  -f    Force updates regardless of compatibility score (requires -u or -p)
  -p    Commit, push, and create a pull request after updating actions (implies -u)
  -b    Branch to update or create before applying changes (requires -u or -p)
  -o    Output findings as json, md, or yaml
  -t    Only update actions from GitHub Marketplace verified creators (requires -u or -p)
  -v    Verbose output - show skipped actions
  -h    Display this help message

`

// ParseOptions retains getopts grouping, stop-at-first-positional, and help behavior.
func ParseOptions(args []string) (Options, error) {
	var o Options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || len(arg) < 2 || arg[0] != '-' {
			break
		}
		for pos := 1; pos < len(arg); pos++ {
			switch arg[pos] {
			case 'c':
				o.Configure = true
			case 'u':
				o.Update = true
			case 's':
				o.Pin = true
			case 'f':
				o.Force = true
			case 'p':
				o.PR = true
				o.Update = true
			case 't':
				o.VerifiedOnly = true
			case 'v':
				o.Verbosity++
			case 'h':
				o.Help = true
				return o, nil
			case 'b', 'o':
				flag := arg[pos]
				value := arg[pos+1:]
				if value == "" {
					i++
					if i >= len(args) {
						o.Help = true
						o.Diagnostic = fmt.Sprintf("Option -%c requires an argument", flag)
						return o, nil
					}
					value = args[i]
				}
				if flag == 'b' {
					o.Branch = value
				} else {
					o.Format = value
				}
				pos = len(arg)
			default:
				o.Help = true
				o.Diagnostic = fmt.Sprintf("Invalid option -%c", arg[pos])
				return o, nil
			}
		}
	}
	if o.Configure && (len(args) != 1 || args[0] != "-c") {
		return o, fmt.Errorf("-c must be used alone")
	}
	if !o.Update {
		if o.Force {
			return o, fmt.Errorf("-f can only be used with -u or -p")
		}
		if o.Branch != "" {
			return o, fmt.Errorf("-b can only be used with -u or -p")
		}
		if o.VerifiedOnly {
			return o, fmt.Errorf("-t can only be used with -u or -p")
		}
	}
	switch o.Format {
	case "", "json", "md", "yaml":
	default:
		return o, fmt.Errorf("-o must be one of: json, md, yaml")
	}
	return o, nil
}
