// Package model defines the values shared by the scanner, policy, and reports.
package model

// Finding preserves the public structured-report contract.
type Finding struct {
	File               string  `json:"file" yaml:"file"`
	Line               int     `json:"line" yaml:"line"`
	Action             string  `json:"action" yaml:"action"`
	Repository         string  `json:"repository" yaml:"repository"`
	Current            string  `json:"current" yaml:"current"`
	Latest             string  `json:"latest" yaml:"latest"`
	CurrentTag         *string `json:"current_tag" yaml:"current_tag"`
	LatestSHA          *string `json:"latest_sha" yaml:"latest_sha"`
	CommitsSince       *int    `json:"commits_since" yaml:"commits_since"`
	UpdateRef          string  `json:"update_ref" yaml:"update_ref"`
	CompatibilityScore any     `json:"compatibility_score" yaml:"compatibility_score"`
	VerifiedCreator    bool    `json:"verified_creator" yaml:"verified_creator"`
	ReleaseNotes       *string `json:"release_notes" yaml:"release_notes"`
}

type Assessment struct {
	Decision     string        `json:"decision"`
	Confidence   string        `json:"confidence"`
	Summary      string        `json:"summary"`
	Findings     []Caution     `json:"findings"`
	Remediations []Remediation `json:"remediations"`
}

type Caution struct {
	Detail   string `json:"caution_detail"`
	Safety   string `json:"safety"`
	Evidence string `json:"evidence"`
}

type Remediation struct {
	File         string `json:"file"`
	Line         int    `json:"line"`
	Input        string `json:"input"`
	Operation    string `json:"operation"`
	CurrentValue string `json:"current_value"`
	NewValue     string `json:"new_value"`
	Reason       string `json:"reason"`
}

func String(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
