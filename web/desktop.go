package webassets

import (
	"fmt"
	"strings"
)

func DesktopHTML() (string, error) {
	html, err := FS.ReadFile("static/index.html")
	if err != nil {
		return "", err
	}
	css, err := FS.ReadFile("static/style.css")
	if err != nil {
		return "", err
	}
	text := strings.Replace(string(html), `<link rel="stylesheet" href="/assets/style.css">`, "<style>"+string(css)+"</style>", 1)
	for _, name := range []string{"studio.css"} {
		data, err := FS.ReadFile("static/" + name)
		if err != nil {
			return "", err
		}
		text = strings.Replace(text, `<link rel="stylesheet" href="/assets/`+name+`">`, "<style>"+string(data)+"</style>", 1)
	}
	for _, name := range []string{"app.js", "connections.js", "pool-editor.js", "studio-interactions.js", "pipeline-view.js"} {
		data, err := FS.ReadFile("static/" + name)
		if err != nil {
			return "", err
		}
		text = strings.Replace(text, `<script src="/assets/`+name+`" defer></script>`, "<script>"+string(data)+"</script>", 1)
	}
	if strings.Contains(text, `src="/assets/`) || strings.Contains(text, `href="/assets/`) {
		return "", fmt.Errorf("unbundled desktop asset")
	}
	return text, nil
}
