# AIOverlay maintained WebView2 patch

Base: github.com/jchv/go-webview2 56598839c808 (MIT, LICENSE retained).

The application owns COM apartment initialization. This fork removes the hidden
init-time thread lock, closes controller/COM references synchronously on the owner
thread, removes HWND context and callback roots, guards dispatch after destruction,
allows initially hidden creation, and returns initialization failures instead of
terminating the host process. No browser runtime is forked. It also exposes
NavigationCompleted (isSuccess + webErrorStatus) through WebView.OnNavigationCompleted
so the host can diagnose documents that load but never present.

Host visibility is forwarded to the controller: WM_SHOWWINDOW (applied after the
show completes), minimize/restore via WM_SIZE and the initial parent state at
controller creation all reach ICoreWebView2Controller::put_IsVisible. WebView2
does not observe ShowWindow on its parent, and without this a WebView created
under a hidden window, or hidden and shown again, could present only its default
white background while the page itself ran. WebViewOptions.Background paints the
host window and the WebView2 default background before the first frame, and
WindowOptions.Positioned/Left/Top place the window before it is shown. The window
procedure callback is created once per process (NewCallback slots are never
released). COM fixes: PermissionRequested passes an out-pointer to
GetPermissionKind, AddAcceleratorKeyPressed passes the token pointer itself,
GetDefaultBackgroundColor returns the colour by value and every AddRef wrapper
passes its `this` pointer.

References: https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/threading-model
https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2controller
https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/icorewebview2navigationcompletedeventargs
