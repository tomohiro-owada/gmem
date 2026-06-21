package gmem

import "testing"

func TestSecurityGateRejectsSecrets(t *testing.T) {
	gate := SecurityGate{Policy: SecurityPolicy{RejectPersonalInformation: true}}
	findings := gate.Check("token", "API_KEY=sk-thisisatesttokenvalue")
	if len(findings) == 0 {
		t.Fatal("expected secret finding")
	}
}

func TestSecurityGateRejectsEmailByDefault(t *testing.T) {
	gate := SecurityGate{Policy: SecurityPolicy{RejectPersonalInformation: true}}
	findings := gate.Check("contact", "mail me at user@example.com")
	if len(findings) == 0 {
		t.Fatal("expected pii finding")
	}
}

func TestSecurityGateCanRelaxEmailPolicy(t *testing.T) {
	gate := SecurityGate{Policy: SecurityPolicy{RejectPersonalInformation: false}}
	findings := gate.Check("contact", "mail me at user@example.com")
	for _, f := range findings {
		if f.Category == "email" {
			t.Fatal("email should be allowed when reject_personal_information is false")
		}
	}
}
