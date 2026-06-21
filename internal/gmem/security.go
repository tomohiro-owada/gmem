package gmem

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

type SecurityFinding struct {
	Category string `json:"category"`
	Field    string `json:"field"`
}

type SecurityGate struct {
	Policy SecurityPolicy
}

var secretRules = []struct {
	category string
	re       *regexp.Regexp
}{
	{"private_key", regexp.MustCompile(`(?i)-----BEGIN (RSA |OPENSSH |EC |DSA |)?PRIVATE KEY-----`)},
	{"aws_access_key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"github_token", regexp.MustCompile(`\b(ghp|github_pat)_[A-Za-z0-9_]{20,}\b`)},
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"slack_token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"bearer_token", regexp.MustCompile(`(?i)\bAuthorization\s*:\s*Bearer\s+[A-Za-z0-9._~+/=-]{10,}`)},
	{"env_secret", regexp.MustCompile(`(?im)^\s*(PASSWORD|SECRET|TOKEN|API_KEY|ACCESS_KEY|PRIVATE_KEY)\s*=\s*.+$`)},
}

var piiRules = []struct {
	category string
	re       *regexp.Regexp
}{
	{"email", regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
	{"phone", regexp.MustCompile(`\b(?:\+?\d{1,3}[-.\s]?)?(?:\(?\d{2,4}\)?[-.\s]?)?\d{3,4}[-.\s]?\d{4}\b`)},
}

func (g SecurityGate) Check(title, content string) []SecurityFinding {
	var findings []SecurityFinding
	findings = append(findings, scanField("title", title, g.Policy)...)
	findings = append(findings, scanField("content", content, g.Policy)...)
	return findings
}

func scanField(field, value string, policy SecurityPolicy) []SecurityFinding {
	var findings []SecurityFinding
	if !utf8.ValidString(value) {
		findings = append(findings, SecurityFinding{Category: "invalid_utf8", Field: field})
	}
	if hasControl(value) {
		findings = append(findings, SecurityFinding{Category: "control_character", Field: field})
	}
	for _, rule := range secretRules {
		if rule.re.MatchString(value) {
			findings = append(findings, SecurityFinding{Category: rule.category, Field: field})
		}
	}
	if policy.RejectPersonalInformation {
		for _, rule := range piiRules {
			if rule.re.MatchString(value) {
				findings = append(findings, SecurityFinding{Category: rule.category, Field: field})
			}
		}
	}
	if policy.RejectOrganizationNames && containsLabel(value, "会社") {
		findings = append(findings, SecurityFinding{Category: "organization_name", Field: field})
	}
	if policy.RejectCustomerNames && containsLabel(value, "顧客") {
		findings = append(findings, SecurityFinding{Category: "customer_name", Field: field})
	}
	return findings
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return true
		}
	}
	return false
}

func containsLabel(s, label string) bool {
	return strings.Contains(s, label+"名") || strings.Contains(s, label+":") || strings.Contains(s, label+"：")
}
