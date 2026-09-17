# AIOverlay maintained WebView2 patch

Base: github.com/jchv/go-webview2 56598839c808 (MIT, LICENSE retained).

The application owns COM apartment initialization. This fork removes the hidden
init-time thread lock, closes controller/COM references synchronously on the owner
thread, removes HWND context and callback roots, guards dispatch after destruction,
allows initially hidden creation, and returns initialization failures instead of
terminating the host process. No browser runtime is forked.

References: https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/threading-model
https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2controller
