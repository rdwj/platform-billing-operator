package walkthroughs

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed definitions/*.yaml
var definitionsFS embed.FS

// Load reads all walkthrough definitions from the embedded filesystem
// and returns them keyed by walkthrough ID.
func Load() (map[string]*Definition, error) {
	return LoadFrom(definitionsFS, "definitions")
}

// LoadFrom reads walkthrough definitions from an arbitrary FS and root
// directory. Useful for testing with in-memory filesystems.
func LoadFrom(fsys fs.FS, root string) (map[string]*Definition, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("reading walkthrough definitions dir: %w", err)
	}

	defs := make(map[string]*Definition, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}

		data, err := fs.ReadFile(fsys, filepath.Join(root, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}

		var def Definition
		if err := yaml.Unmarshal(data, &def); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", e.Name(), err)
		}

		if err := validate(&def, e.Name()); err != nil {
			return nil, err
		}

		defs[def.ID] = &def
	}

	if len(defs) == 0 {
		return nil, fmt.Errorf("no walkthrough definitions found in %s", root)
	}

	return defs, nil
}

func validate(def *Definition, filename string) error {
	if def.ID == "" {
		return fmt.Errorf("%s: missing required field 'id'", filename)
	}
	if def.Title == "" {
		return fmt.Errorf("%s: missing required field 'title'", filename)
	}
	if len(def.Steps) == 0 {
		return fmt.Errorf("%s: walkthrough must have at least one step", filename)
	}

	stepIDs := make(map[string]struct{}, len(def.Steps))
	for i, step := range def.Steps {
		if step.ID == "" {
			return fmt.Errorf("%s: step %d missing required field 'id'", filename, i)
		}
		if step.Title == "" {
			return fmt.Errorf("%s: step %q missing required field 'title'", filename, step.ID)
		}
		if _, exists := stepIDs[step.ID]; exists {
			return fmt.Errorf("%s: duplicate step id %q", filename, step.ID)
		}
		stepIDs[step.ID] = struct{}{}
	}

	return nil
}
