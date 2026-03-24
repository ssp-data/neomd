package render

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// htmlTemplate is a minimal, self-contained email wrapper.
// Derived from the listmonk template at:
// /home/sspaeti/git/sspaeti.com/listmonk/misc/email-template.html
const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
line-height:1.6;color:#333;margin:0;padding:0}
.w{max-width:650px;margin:0 auto;padding:20px}
a{color:#ff5d62;text-decoration:none}
a:hover{text-decoration:underline}
hr{border:0;border-bottom:1px solid #eaeaea;margin:25px 0}
h1{font-size:24px;color:#24292e;margin-top:1.5em;margin-bottom:.5em;line-height:1.3}
h2{font-size:20px;color:#24292e;margin-top:1.5em;margin-bottom:.5em;line-height:1.3}
h3{font-size:18px;color:#24292e;margin-top:1.5em;margin-bottom:.5em;line-height:1.3}
p,ul,li{font-size:16px;margin-bottom:1em}
code{background:#f6f8fa;padding:2px 5px;border-radius:3px;
font-family:SFMono-Regular,Consolas,"Liberation Mono",Menlo,monospace;font-size:85%%}
pre{background:#f6f8fa;padding:16px;border-radius:6px;overflow:auto;
font-family:SFMono-Regular,Consolas,"Liberation Mono",Menlo,monospace;font-size:85%%;line-height:1.45}
blockquote{border-left:3px solid #e1e4e8;color:#6a737d;margin-left:0;padding-left:1em}
img{max-width:100%%;height:auto}
</style>
</head>
<body>
<div class="w">
%s
</div>
</body>
</html>`

// md is the goldmark renderer with GFM extensions.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// ToHTML converts a Markdown string to a complete HTML email document.
func ToHTML(markdown string) (string, error) {
	var fragment bytes.Buffer
	if err := md.Convert([]byte(markdown), &fragment); err != nil {
		return "", fmt.Errorf("markdown to html: %w", err)
	}
	return fmt.Sprintf(htmlTemplate, fragment.String()), nil
}
