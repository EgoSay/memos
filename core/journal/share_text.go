package journal

import (
	"regexp"
	"strings"
)

var journalImage = regexp.MustCompile(`!\[([^\]]*)\]\([^\n)]*\)`)
var journalHTML = regexp.MustCompile(`<[^>]*>`)
var journalPrivatePath = regexp.MustCompile(`(?i)(?:https?://[^\s<>"()]+)?/(?:file|api/v1|memos|attachments)/[^\s<>"()\]]+`)

// ShareText removes automatic embed sources and internal resource URLs. It never
// rewrites the stored original; visitors receive a safe reading representation.
func ShareText(content string) string {
	content = journalImage.ReplaceAllString(content, "$1（图片见附件）")
	content = journalPrivatePath.ReplaceAllString(content, "（内部引用已隐藏）")
	return strings.TrimSpace(journalHTML.ReplaceAllString(content, ""))
}
