#!/usr/bin/env bash
# labels.sh - Create GitHub labels from labeler.yml and pr-labels.yml
#
# This script extracts label names from .github/labeler.yml and
# .github/workflows/pr-labels.yml, then creates them in the GitHub repository
# if they don't already exist.
#
# Usage:
#   ./tools/labels.sh [--dry-run] [--force]
#
# Options:
#   --dry-run    Show what would be created without making changes
#   --force      Recreate labels even if they exist (updates color/description)
#   --help       Show this help message

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Script options
DRY_RUN=false
FORCE=false

# Label definitions with colors and descriptions
# Colors follow GitHub's semantic label color scheme
declare -A LABEL_COLORS=(
  # From labeler.yml - content/category labels
  ["go"]="0E8A16"            # Green - code
  ["dependencies"]="0366D6"  # Blue - dependencies
  ["documentation"]="0075CA" # Light blue - docs
  ["ci"]="C5DEF5"            # Pale blue - automation
  ["ai"]="D876E3"            # Purple - AI/ML
  ["repo"]="EDEDED"          # Gray - repo config
  ["docker"]="0DB7ED"        # Docker blue
  ["feature"]="A2EEEF"       # Light cyan - feature

  # From pr-labels.yml - semver labels
  ["major"]="D73A4A"        # Red - breaking
  ["minor"]="0E8A16"        # Green - feature
  ["patch"]="FEF2C0"        # Yellow - fix
  ["dont-release"]="D4C5F9" # Lavender - skip
)

declare -A LABEL_DESCRIPTIONS=(
  # From labeler.yml
  ["go"]="Changes to Go code (cmd/, internal/, pkg/)"
  ["dependencies"]="Dependency updates (go.mod, go.sum, mise.toml)"
  ["documentation"]="Documentation changes (docs/, README.md)"
  ["ci"]="CI/CD pipeline changes (.github/)"
  ["ai"]="AI-related changes (docs/ai/, CLAUDE.md)"
  ["repo"]="Repository configuration (linters, codecov, etc.)"
  ["docker"]="Docker-related changes (Dockerfile)"
  ["feature"]="New feature or enhancement"

  # From pr-labels.yml
  ["major"]="Breaking changes - increment major version (x.0.0)"
  ["minor"]="New features - increment minor version (0.x.0)"
  ["patch"]="Bug fixes - increment patch version (0.0.x)"
  ["dont-release"]="No release needed for this PR"
)

# Script directory and repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# File paths
LABELER_FILE="${REPO_ROOT}/.github/labeler.yml"
PR_LABELS_FILE="${REPO_ROOT}/.github/workflows/pr-labels.yml"

# Functions
log_info() {
  echo -e "${BLUE}ℹ${NC} $*"
}

log_success() {
  echo -e "${GREEN}✓${NC} $*"
}

log_warning() {
  echo -e "${YELLOW}⚠${NC} $*"
}

log_error() {
  echo -e "${RED}✗${NC} $*" >&2
}

show_help() {
  sed -n '/^# labels.sh/,/^$/p' "$0" | sed 's/^# \?//'
  exit 0
}

check_dependencies() {
  local missing_deps=()

  if ! command -v gh &>/dev/null; then
    missing_deps+=("gh (GitHub CLI)")
  fi

  if ! command -v yq &>/dev/null; then
    log_warning "yq not found - will use grep for YAML parsing (less robust)"
  fi

  if [ ${#missing_deps[@]} -gt 0 ]; then
    log_error "Missing required dependencies: ${missing_deps[*]}"
    log_error "Install with: brew install gh yq"
    exit 1
  fi
}

check_repo() {
  if ! git rev-parse --git-dir &>/dev/null; then
    log_error "Not in a git repository"
    exit 1
  fi

  if ! gh repo view &>/dev/null; then
    log_error "Not in a GitHub repository or not authenticated"
    log_error "Run: gh auth login"
    exit 1
  fi
}

extract_labels_from_labeler() {
  local file="$1"

  if [ ! -f "$file" ]; then
    log_warning "File not found: $file"
    return
  fi

  # Extract labels (keys at the start of lines, followed by colon)
  # This captures top-level keys in the YAML
  grep -E '^[a-z-]+:' "$file" | cut -d: -f1 | sort -u
}

extract_labels_from_pr_workflow() {
  local file="$1"

  if [ ! -f "$file" ]; then
    log_warning "File not found: $file"
    return
  fi

  # Extract labels from the "labels:" line in the workflow
  # Format: labels: "major, minor, patch, dont-release"
  # Using sed for macOS compatibility (BSD grep doesn't support -P)
  sed -n 's/.*labels:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | sort -u
}

get_existing_labels() {
  # Get all existing label names from the repo
  gh label list --limit 1000 --json name --jq '.[].name'
}

label_exists() {
  local label="$1"
  local existing_labels="$2"

  echo "$existing_labels" | grep -q "^${label}$"
}

create_label() {
  local label="$1"
  local color="${LABEL_COLORS[$label]:-EDEDED}"
  local description="${LABEL_DESCRIPTIONS[$label]:-}"

  if [ "$DRY_RUN" = true ]; then
    echo -e "  ${BLUE}[DRY RUN]${NC} Would create: ${label} (color: #${color})"
    [ -n "$description" ] && echo -e "            Description: ${description}"
    return 0
  fi

  if gh label create "$label" --color "$color" --description "$description" 2>/dev/null; then
    log_success "Created label: ${label}"
    return 0
  else
    log_error "Failed to create label: ${label}"
    return 1
  fi
}

update_label() {
  local label="$1"
  local color="${LABEL_COLORS[$label]:-EDEDED}"
  local description="${LABEL_DESCRIPTIONS[$label]:-}"

  if [ "$DRY_RUN" = true ]; then
    echo -e "  ${BLUE}[DRY RUN]${NC} Would update: ${label} (color: #${color})"
    [ -n "$description" ] && echo -e "            Description: ${description}"
    return 0
  fi

  if gh label edit "$label" --color "$color" --description "$description" 2>/dev/null; then
    log_success "Updated label: ${label}"
    return 0
  else
    log_error "Failed to update label: ${label}"
    return 1
  fi
}

main() {
  # Parse arguments
  while [[ $# -gt 0 ]]; do
    case $1 in
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --force)
      FORCE=true
      shift
      ;;
    --help | -h)
      show_help
      ;;
    *)
      log_error "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
    esac
  done

  log_info "GitHub Label Management Script"
  echo

  # Pre-flight checks
  check_dependencies
  check_repo

  # Get repo info
  local repo_name
  repo_name=$(gh repo view --json nameWithOwner --jq '.nameWithOwner')
  log_info "Repository: ${repo_name}"

  if [ "$DRY_RUN" = true ]; then
    log_warning "DRY RUN MODE - No changes will be made"
  fi
  echo

  # Extract labels from both files
  log_info "Extracting labels from configuration files..."

  local labeler_labels
  labeler_labels=$(extract_labels_from_labeler "$LABELER_FILE")
  log_info "Found $(echo "$labeler_labels" | wc -l | tr -d ' ') labels in labeler.yml"

  local pr_labels
  pr_labels=$(extract_labels_from_pr_workflow "$PR_LABELS_FILE")
  log_info "Found $(echo "$pr_labels" | wc -l | tr -d ' ') labels in pr-labels.yml"

  # Combine and deduplicate
  local all_labels
  all_labels=$(echo -e "${labeler_labels}\n${pr_labels}" | sort -u)
  local total_labels
  total_labels=$(echo "$all_labels" | wc -l | tr -d ' ')

  log_info "Total unique labels: ${total_labels}"
  echo

  # Get existing labels
  log_info "Fetching existing labels from repository..."
  local existing_labels
  existing_labels=$(get_existing_labels)
  local existing_count
  existing_count=$(echo "$existing_labels" | grep -c . || echo "0")
  log_info "Repository has ${existing_count} existing labels"
  echo

  # Process each label
  local created=0
  local updated=0
  local skipped=0
  local failed=0

  log_info "Processing labels..."
  echo

  while IFS= read -r label; do
    [ -z "$label" ] && continue

    if label_exists "$label" "$existing_labels"; then
      if [ "$FORCE" = true ]; then
        if update_label "$label"; then
          updated=$((updated + 1))
        else
          failed=$((failed + 1))
        fi
      else
        log_info "Label already exists: ${label} (use --force to update)"
        skipped=$((skipped + 1))
      fi
    else
      if create_label "$label"; then
        created=$((created + 1))
      else
        failed=$((failed + 1))
      fi
    fi
  done <<<"$all_labels"

  # Summary
  echo
  log_info "Summary:"
  echo "  Created:  ${created}"
  echo "  Updated:  ${updated}"
  echo "  Skipped:  ${skipped}"
  echo "  Failed:   ${failed}"
  echo "  Total:    ${total_labels}"

  if [ "$failed" -gt 0 ]; then
    echo
    log_error "Some labels failed to create/update"
    exit 1
  fi

  if [ "$DRY_RUN" = false ] && [ "$created" -gt 0 ]; then
    echo
    log_success "Labels created successfully!"
    log_info "View labels at: https://github.com/${repo_name}/labels"
  fi
}

main "$@"
