module aioverlay

go 1.27.1

require github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808

require (
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	golang.org/x/sys v0.44.0 // indirect
)

replace github.com/jchv/go-webview2 => ./third_party/go-webview2
