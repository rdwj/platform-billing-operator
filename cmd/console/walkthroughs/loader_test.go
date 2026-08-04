package walkthroughs

import (
	"testing"
	"testing/fstest"
)

func TestLoad_EmbeddedDefinitions(t *testing.T) {
	defs, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	for _, tc := range []struct {
		id        string
		title     string
		stepCount int
		stepIDs   []string
	}{
		{
			id:        "stripe",
			title:     "Connect Stripe",
			stepCount: 4,
			stepIDs:   []string{"credentials", "webhook", "reload-dry-run", "enable"},
		},
		{
			id:        "generic-webhook",
			title:     "Connect Generic Webhook",
			stepCount: 2,
			stepIDs:   []string{"configure", "verify"},
		},
	} {
		t.Run(tc.id, func(t *testing.T) {
			def, ok := defs[tc.id]
			if !ok {
				t.Fatalf("definition %q not found", tc.id)
			}
			if def.Title != tc.title {
				t.Errorf("title = %q, want %q", def.Title, tc.title)
			}
			if len(def.Steps) != tc.stepCount {
				t.Fatalf("step count = %d, want %d", len(def.Steps), tc.stepCount)
			}
			for i, wantID := range tc.stepIDs {
				if def.Steps[i].ID != wantID {
					t.Errorf("step[%d].ID = %q, want %q", i, def.Steps[i].ID, wantID)
				}
			}
		})
	}
}

func TestLoad_StripeParsesCorrectly(t *testing.T) {
	defs, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	stripe := defs["stripe"]

	if len(stripe.Prereqs) != 2 {
		t.Fatalf("prereqs count = %d, want 2", len(stripe.Prereqs))
	}
	if stripe.Prereqs[0].Check != "crdExists" {
		t.Errorf("prereqs[0].Check = %q, want %q", stripe.Prereqs[0].Check, "crdExists")
	}

	creds := stripe.Steps[0]
	if len(creds.Form) != 1 {
		t.Fatalf("credentials step form count = %d, want 1", len(creds.Form))
	}
	if creds.Form[0].Kind != "secret" {
		t.Errorf("credentials form[0].Kind = %q, want %q", creds.Form[0].Kind, "secret")
	}
	if len(creds.Writes) != 2 {
		t.Fatalf("credentials step writes count = %d, want 2", len(creds.Writes))
	}
	if creds.Verify == nil {
		t.Fatal("credentials step verify is nil")
	}
	if creds.Verify.Condition == nil {
		t.Fatal("credentials step verify.condition is nil")
	}
	if creds.Verify.Condition.Type != "CredentialsValid" {
		t.Errorf("verify.condition.type = %q, want %q", creds.Verify.Condition.Type, "CredentialsValid")
	}

	enable := stripe.Steps[3]
	if len(enable.Writes) != 1 {
		t.Fatalf("enable step writes count = %d, want 1", len(enable.Writes))
	}
	if enable.Writes[0].Patch == "" {
		t.Error("enable step writes[0].patch is empty, expected a patch template")
	}
}

func TestLoad_GenericWebhookParsesCorrectly(t *testing.T) {
	defs, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	gw := defs["generic-webhook"]

	configure := gw.Steps[0]
	if len(configure.Form) != 2 {
		t.Fatalf("configure step form count = %d, want 2", len(configure.Form))
	}

	var hasSecret bool
	for _, f := range configure.Form {
		if f.Kind == "secret" {
			hasSecret = true
		}
	}
	if !hasSecret {
		t.Error("configure step should have a secret form field")
	}

	verify := gw.Steps[1]
	if verify.Verify == nil || verify.Verify.Action == nil {
		t.Fatal("verify step should have a verify action")
	}
}

func TestLoad_ToMetadata(t *testing.T) {
	defs, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	m := defs["stripe"].ToMetadata()
	if m.ID != "stripe" || m.Title != "Connect Stripe" || m.StepCount != 4 {
		t.Errorf("metadata = %+v, want id=stripe title='Connect Stripe' stepCount=4", m)
	}
}

func TestLoadFrom_ValidationErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing id",
			yaml:    "title: Test\nsteps:\n  - id: s1\n    title: Step 1\n",
			wantErr: "missing required field 'id'",
		},
		{
			name:    "missing title",
			yaml:    "id: test\nsteps:\n  - id: s1\n    title: Step 1\n",
			wantErr: "missing required field 'title'",
		},
		{
			name:    "no steps",
			yaml:    "id: test\ntitle: Test\nsteps: []\n",
			wantErr: "must have at least one step",
		},
		{
			name:    "step missing id",
			yaml:    "id: test\ntitle: Test\nsteps:\n  - title: Step 1\n",
			wantErr: "step 0 missing required field 'id'",
		},
		{
			name:    "step missing title",
			yaml:    "id: test\ntitle: Test\nsteps:\n  - id: s1\n",
			wantErr: "step \"s1\" missing required field 'title'",
		},
		{
			name:    "duplicate step id",
			yaml:    "id: test\ntitle: Test\nsteps:\n  - id: s1\n    title: One\n  - id: s1\n    title: Two\n",
			wantErr: "duplicate step id \"s1\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fstest.MapFS{
				"defs/bad.yaml": {Data: []byte(tc.yaml)},
			}
			_, err := LoadFrom(fs, "defs")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); !contains(got, tc.wantErr) {
				t.Errorf("error = %q, want substring %q", got, tc.wantErr)
			}
		})
	}
}

func TestLoadFrom_EmptyDir(t *testing.T) {
	fs := fstest.MapFS{
		"defs/.keep": {Data: []byte{}},
	}
	_, err := LoadFrom(fs, "defs")
	if err == nil {
		t.Fatal("expected error for empty definitions dir")
	}
}

func TestLoadFrom_InvalidYAML(t *testing.T) {
	fs := fstest.MapFS{
		"defs/bad.yaml": {Data: []byte("{{invalid yaml")},
	}
	_, err := LoadFrom(fs, "defs")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
