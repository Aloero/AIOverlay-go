package engine

import "aioverlay/internal/domain"

func (r *runner) contextualizeNodeInput(node domain.Node, input nodeValue) nodeValue {
	mode := node.ContextMode
	if mode == "" && node.ContextID != "" {
		mode = "named"
	}
	switch mode {
	case "none":
		input.contextText = ""
		if !input.imagePinned {
			input.image = nil
			input.imageResolved = false
		}
		return input
	case "named":
		view, ok := r.input.ContextViews[node.ContextID]
		if !ok {
			input.contextText = ""
			if !input.imagePinned {
				input.image = nil
				input.imageResolved = false
			}
			return input
		}
		input.contextText = view.Text
		if !input.imagePinned {
			input.image = append([]byte(nil), view.ImageJPEG...)
			input.imageResolved = false
		}
	}
	return input
}
