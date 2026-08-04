package walkthroughs

// Definition describes an integration walkthrough: a sequence of steps
// that guide a provider through connecting a payment service or
// external system to the billing operator.
type Definition struct {
	ID      string   `json:"id"      yaml:"id"`
	Title   string   `json:"title"   yaml:"title"`
	Prereqs []Prereq `json:"prereqs" yaml:"prereqs"`
	Steps   []Step   `json:"steps"   yaml:"steps"`
}

// Prereq is a precondition checked before a walkthrough can begin.
type Prereq struct {
	Check string `json:"check,omitempty" yaml:"check,omitempty"`
	Kind  string `json:"kind,omitempty"  yaml:"kind,omitempty"`
	Text  string `json:"text,omitempty"  yaml:"text,omitempty"`
}

// Step is one phase of the walkthrough.
type Step struct {
	ID      string        `json:"id"                yaml:"id"`
	Title   string        `json:"title"             yaml:"title"`
	Text    string        `json:"text,omitempty"    yaml:"text,omitempty"`
	Form    []FormField   `json:"form,omitempty"    yaml:"form,omitempty"`
	Display []DisplayItem `json:"display,omitempty" yaml:"display,omitempty"`
	Writes  []WriteSpec   `json:"writes,omitempty"  yaml:"writes,omitempty"`
	Verify  *VerifySpec   `json:"verify,omitempty"  yaml:"verify,omitempty"`
}

// FormField describes one input the user fills in during a step.
type FormField struct {
	Name      string `json:"name"                yaml:"name"`
	Kind      string `json:"kind,omitempty"      yaml:"kind,omitempty"`
	SecretKey string `json:"secretKey,omitempty" yaml:"secretKey,omitempty"`
	Help      string `json:"help,omitempty"      yaml:"help,omitempty"`
	Validate  string `json:"validate,omitempty"  yaml:"validate,omitempty"`
	Label     string `json:"label,omitempty"     yaml:"label,omitempty"`
}

// DisplayItem is a read-only value shown during a step, optionally
// with a nested form field for additional input.
type DisplayItem struct {
	Label    string     `json:"label"              yaml:"label"`
	From     string     `json:"from,omitempty"     yaml:"from,omitempty"`
	Value    string     `json:"value,omitempty"    yaml:"value,omitempty"`
	Copyable bool       `json:"copyable,omitempty" yaml:"copyable,omitempty"`
	Form     *FormField `json:"form,omitempty"     yaml:"form,omitempty"`
}

// WriteSpec describes a Kubernetes resource created or patched by a step.
type WriteSpec struct {
	Kind     string `json:"kind"               yaml:"kind"`
	Name     string `json:"name"               yaml:"name"`
	Template string `json:"template,omitempty" yaml:"template,omitempty"`
	Patch    string `json:"patch,omitempty"    yaml:"patch,omitempty"`
}

// VerifySpec describes how the BFF checks that a step completed.
type VerifySpec struct {
	Condition    *ConditionProbe  `json:"condition,omitempty"    yaml:"condition,omitempty"`
	BFFCall      string           `json:"bffCall,omitempty"      yaml:"bffCall,omitempty"`
	Expects      map[string]any   `json:"expects,omitempty"      yaml:"expects,omitempty"`
	Action       *VerifyAction    `json:"action,omitempty"       yaml:"action,omitempty"`
	Timeout      string           `json:"timeout,omitempty"      yaml:"timeout,omitempty"`
	FailureHints map[string]string `json:"failureHints,omitempty" yaml:"failureHints,omitempty"`
}

// ConditionProbe checks a condition on a Kubernetes resource.
type ConditionProbe struct {
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	Type string `json:"type"           yaml:"type"`
}

// VerifyAction is an optional user-triggered action during verification.
type VerifyAction struct {
	Button  string `json:"button"  yaml:"button"`
	BFFCall string `json:"bffCall" yaml:"bffCall"`
}

// Metadata is the subset of a Definition returned by the list endpoint.
type Metadata struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	StepCount int    `json:"stepCount"`
}

// ToMetadata extracts the list-view metadata from a full definition.
func (d *Definition) ToMetadata() Metadata {
	return Metadata{
		ID:        d.ID,
		Title:     d.Title,
		StepCount: len(d.Steps),
	}
}
