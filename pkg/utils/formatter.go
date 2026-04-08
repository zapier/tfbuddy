package utils

import (
	"fmt"
	"log"
	"strings"
)

// We format the URL as **RUN URL**: <url> <br> under tfc_status_update.go
const (
	URL_RUN_PREFIX       = "**Run URL**: "
	URL_RUN_SUFFIX       = "<br>"
	URL_RUN_GROUP_PREFIX = `<details><summary>

### Previous TFC Urls:

</summary>

| Run ID | Status | Created at |
| ------ | ------ | ---------- |`
	URL_RUN_GROUP_SUFFIX  = "</details>"
	URL_RUN_STATUS_PREFIX = "**Status**: "

	TFBUDDY_MARKER_PREFIX = "<!-- tfbuddy:ws="
	TFBUDDY_MARKER_SUFFIX = " -->"
)

// FormatTFBuddyMarker returns an invisible HTML comment that tags a note
// with its workspace and action so cleanup can be workspace-scoped.
func FormatTFBuddyMarker(workspace, action string) string {
	return fmt.Sprintf("%s%s:action=%s%s", TFBUDDY_MARKER_PREFIX, workspace, action, TFBUDDY_MARKER_SUFFIX)
}

// ParseTFBuddyMarker extracts (workspace, action) from the HTML comment
// marker embedded in a note body.  Returns found=false when the marker is
// absent or malformed.
func ParseTFBuddyMarker(body string) (workspace, action string, found bool) {
	raw := CaptureSubstring(body, TFBUDDY_MARKER_PREFIX, TFBUDDY_MARKER_SUFFIX)
	if raw == "" {
		return "", "", false
	}
	parts := strings.SplitN(raw, ":action=", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func CaptureSubstring(body string, prefix string, suffix string) string {
	startIndex := strings.Index(body, prefix)
	if startIndex == -1 {
		return ""
	}

	subBody := body[startIndex+len(prefix):]
	endIndex := strings.Index(subBody, suffix)
	if endIndex == -1 {
		return ""
	}

	return subBody[:endIndex]
}

// AI generated these, may not be compresphenive
func FormatStatus(status string) string {
	status = strings.Trim(status, "`")
	log.Printf("Status: %s", status)
	switch status {
	case "applied":
		return "✅ Applied"
	case "planned":
		return "📝 Planned"
	case "policy_checked":
		return "📝 Policy Checked"
	case "policy_soft_failed":
		return "📝 Policy Soft Failed"
	case "errored":
		return "❌ Errored"
	case "canceled":
		return "❌ Canceled"
	case "discarded":
		return "❌ Discarded"
	case "planned_and_finished":
		return "📝 Planned and Finished"
	default:
		// anything we can't match just preserve with the ticks
		return fmt.Sprintf("`%s`", status)
	}
}
