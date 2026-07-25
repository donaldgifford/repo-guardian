package reconciler

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// labelFile is the YAML schema for a label sync configuration file.
type labelFile struct {
	Labels []labelEntry `yaml:"labels"`
}

// labelEntry defines a single label in the label sync configuration.
type labelEntry struct {
	Name        string `yaml:"name"`
	Color       string `yaml:"color"`
	Description string `yaml:"description"`
	RenamedFrom string `yaml:"renamed_from"`
}

// labelSyncReconciler syncs repository labels from a YAML configuration file.
type labelSyncReconciler struct {
	deleteExtra bool
}

// NewLabelSyncReconciler creates a label sync reconciler from the given config.
func NewLabelSyncReconciler(config policy.ReconcilerConfig) (Reconciler, error) {
	return &labelSyncReconciler{
		deleteExtra: config.DeleteExtra,
	}, nil
}

func (*labelSyncReconciler) Name() string { return "label_sync" }

// RunsOnAbsence reports false: a missing labels file means "no
// desired state declared", not "delete every label".
func (*labelSyncReconciler) RunsOnAbsence() bool { return false }

func (r *labelSyncReconciler) Reconcile(ctx context.Context, params *ReconcileParams) error {
	log := params.Logger

	var lf labelFile
	if err := yaml.Unmarshal([]byte(params.Content), &lf); err != nil {
		return fmt.Errorf("parsing label file: %w", err)
	}

	if len(lf.Labels) == 0 {
		log.Info("label file is empty, nothing to sync")
		return nil
	}

	current, err := params.Client.ListLabels(ctx, params.Owner, params.Repo)
	if err != nil {
		return fmt.Errorf("listing labels: %w", err)
	}

	currentByName := make(map[string]*ghclient.Label, len(current))
	for _, l := range current {
		currentByName[strings.ToLower(l.Name)] = l
	}

	// Process renames first.
	for i := range lf.Labels {
		entry := &lf.Labels[i]
		if entry.RenamedFrom == "" {
			continue
		}

		if err := r.processRename(ctx, params, currentByName, entry); err != nil {
			return err
		}
	}

	// Process creates and updates.
	desiredNames := make(map[string]bool, len(lf.Labels))

	for i := range lf.Labels {
		entry := &lf.Labels[i]
		desiredNames[strings.ToLower(entry.Name)] = true

		if err := r.createOrUpdateLabel(ctx, params, currentByName, entry); err != nil {
			return err
		}
	}

	// Delete extra labels if configured.
	if r.deleteExtra {
		for _, l := range current {
			if desiredNames[strings.ToLower(l.Name)] {
				continue
			}

			log.Info("deleting extra label", "label", l.Name)

			if params.DryRun {
				log.Info("dry run: would delete label", "label", l.Name)
				continue
			}

			if err := params.Client.DeleteLabel(ctx, params.Owner, params.Repo, l.Name); err != nil {
				return fmt.Errorf("deleting label %q: %w", l.Name, err)
			}
		}
	}

	return nil
}

func (*labelSyncReconciler) processRename(
	ctx context.Context,
	params *ReconcileParams,
	currentByName map[string]*ghclient.Label,
	entry *labelEntry,
) error {
	log := params.Logger
	oldKey := strings.ToLower(entry.RenamedFrom)
	newKey := strings.ToLower(entry.Name)

	oldLabel, oldExists := currentByName[oldKey]
	_, newExists := currentByName[newKey]

	if !oldExists || newExists {
		if !oldExists {
			log.Debug("rename source does not exist, skipping", "from", entry.RenamedFrom)
		} else {
			log.Warn("rename target already exists, skipping rename",
				"from", entry.RenamedFrom, "to", entry.Name)
		}

		return nil
	}

	log.Info("renaming label", "from", entry.RenamedFrom, "to", entry.Name)

	if params.DryRun {
		log.Info("dry run: would rename label", "from", entry.RenamedFrom, "to", entry.Name)
		return nil
	}

	newLabel := &ghclient.Label{
		Name:        entry.Name,
		Color:       normalizeColor(entry.Color, oldLabel.Color),
		Description: entry.Description,
	}

	if err := params.Client.UpdateLabel(ctx, params.Owner, params.Repo, entry.RenamedFrom, newLabel); err != nil {
		return fmt.Errorf("renaming label %q to %q: %w", entry.RenamedFrom, entry.Name, err)
	}

	// Update the lookup map.
	delete(currentByName, oldKey)
	currentByName[newKey] = newLabel

	return nil
}

func (*labelSyncReconciler) createOrUpdateLabel(
	ctx context.Context,
	params *ReconcileParams,
	currentByName map[string]*ghclient.Label,
	entry *labelEntry,
) error {
	log := params.Logger
	key := strings.ToLower(entry.Name)
	existing, exists := currentByName[key]

	desired := &ghclient.Label{
		Name:        entry.Name,
		Color:       normalizeColor(entry.Color, ""),
		Description: entry.Description,
	}

	if !exists {
		log.Info("creating label", "label", entry.Name)

		if params.DryRun {
			log.Info("dry run: would create label", "label", entry.Name)
			return nil
		}

		if err := params.Client.CreateLabel(ctx, params.Owner, params.Repo, desired); err != nil {
			return fmt.Errorf("creating label %q: %w", entry.Name, err)
		}

		currentByName[key] = desired

		return nil
	}

	if labelsEqual(existing, desired) {
		return nil
	}

	log.Info("updating label", "label", entry.Name)

	if params.DryRun {
		log.Info("dry run: would update label", "label", entry.Name)
		return nil
	}

	if err := params.Client.UpdateLabel(ctx, params.Owner, params.Repo, entry.Name, desired); err != nil {
		return fmt.Errorf("updating label %q: %w", entry.Name, err)
	}

	currentByName[key] = desired

	return nil
}

// normalizeColor strips a leading '#' from the color string.
func normalizeColor(color, fallback string) string {
	c := strings.TrimPrefix(color, "#")
	if c == "" {
		return fallback
	}

	return c
}

func labelsEqual(a, b *ghclient.Label) bool {
	return strings.EqualFold(a.Name, b.Name) &&
		strings.EqualFold(a.Color, b.Color) &&
		a.Description == b.Description
}
