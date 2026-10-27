package custom_properties

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/template"
)

//go:embed workflow.yml.tmpl
var workflowBody string

var workflowTemplate = func() *template.Compiled {
	c, err := template.NewRenderer().Parse("custom_properties/workflow", workflowBody)
	if err != nil {
		panic(err)
	}

	return c
}()

// WorkflowPath is the workflow file a definition owns under apply =
// "workflow" (D15).
func WorkflowPath(def control.Definition) string { //nolint:gocritic // hugeParam: WorkflowApply's shape
	return ".github/workflows/repo-guardian-" + def.ID.Slug + ".yml"
}

// workflowProperty is one property the workflow writes.
type workflowProperty struct {
	Name  string
	Value string
	Clear bool
}

// WorkflowFile renders the workflow that applies changes on merge: one
// PATCH of every property, a nil value cleared with a JSON null.
// Values pass through the env block only (yamlq, propenv), never inline
// in the script, so a hostile catalog value stays inert.
//
//nolint:gocritic // hugeParam: WorkflowApply's signature.
func (Type) WorkflowFile(def control.Definition, changes []control.APIChange) (control.FileChange, error) {
	props := make([]workflowProperty, 0, len(changes))

	for i := range changes {
		ch := &changes[i]
		if ch.Resource.Kind != control.KindProperty {
			return control.FileChange{}, fmt.Errorf("%s: a %s change cannot be applied by workflow", def.ID, ch.Resource.Kind)
		}

		p := workflowProperty{Name: ch.Resource.Key}

		switch v := ch.Value.(type) {
		case nil:
			p.Clear = true
		case *string:
			if v == nil {
				p.Clear = true
			} else {
				p.Value = *v
			}
		default:
			return control.FileChange{}, fmt.Errorf("%s: property %s value is %T, want *string", def.ID, p.Name, ch.Value)
		}

		props = append(props, p)
	}

	slices.SortFunc(props, func(a, b workflowProperty) int { return strings.Compare(a.Name, b.Name) })

	body, err := workflowTemplate.Render(struct{ Properties []workflowProperty }{props})
	if err != nil {
		return control.FileChange{}, fmt.Errorf("%s: rendering the workflow: %w", def.ID, err)
	}

	return control.FileChange{Path: WorkflowPath(def), Content: []byte(body), Reason: "apply_workflow"}, nil
}
