// Package safety shares the redaction and remediation input protections.
package safety

import "regexp"

var acronymBoundary = regexp.MustCompile(`([A-Z])([A-Z][a-z])`)
var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
var sensitive = regexp.MustCompile(`(?i)(^|[-_.])(token|password|secret|credentials?|api[-_.]?key|private[-_.]?key|ssh[-_.]?key|auth|authorization)([-_.]|$)`)
var secretReference = regexp.MustCompile(`(?i)\bsecrets\b`)

func SensitiveName(name string) bool {
	name = acronymBoundary.ReplaceAllString(name, "${1}_${2}")
	name = camelBoundary.ReplaceAllString(name, "${1}_${2}")
	return sensitive.MatchString(name)
}
func SecretReference(value string) bool { return secretReference.MatchString(value) }
