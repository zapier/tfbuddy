package utils

import (
	"fmt"
	"strings"
)

// We format the URL as **RUN URL**: <url> <br> under tfc_status_update.go
const (
	URL_RUN_PREFIX        = "**Run URL**: "
	URL_RUN_SUFFIX        = "<br>"
	URL_RUN_GROUP_PREFIX  = "<details><summary>Previous TFC URLS</summary>\n"
	URL_RUN_GROUP_SUFFIX  = "</details>"
	URL_RUN_STATUS_PREFIX = "**Status**: "
)

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

// Try and find the "Run URL:" string in the body and return it
// If we can't find it, return nothing
func CaptureRunURLFromBody(body string) string {
	// First, see if we already have a grouping, which means this has run before
	startIndex := strings.Index(body, URL_RUN_PREFIX)
	if startIndex == -1 {
		// Can't find a URL to pull out, so we'll just return
		return ""
	}

	// At this point we've found a starting index, and se tthe appropriate suffix, now to try and pull a URL out
	subBody := body[startIndex+len(URL_RUN_PREFIX):]
	endIndex := strings.Index(subBody, URL_RUN_SUFFIX)
	if endIndex == -1 {
		return "" // Couldn't determine where to cut
	}

	return subBody[:endIndex]
}
