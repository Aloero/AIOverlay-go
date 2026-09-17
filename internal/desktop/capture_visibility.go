package desktop

// A minimized Studio is already absent from the capture. Hiding/restoring it
// would unexpectedly reopen the settings window after a global screenshot.
func hideForCapture(visible, minimized bool) bool { return visible && !minimized }
