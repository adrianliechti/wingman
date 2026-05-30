package provider

func FlattenTools(tools []Tool) []Tool {
	if !hasNested(tools) {
		return tools
	}

	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if len(t.Tools) == 0 {
			out = append(out, t)
			continue
		}
		for _, child := range t.Tools {
			flat := child
			flat.Name = t.Name + "_" + child.Name
			flat.Namespace = t.Name
			flat.Tools = nil
			out = append(out, flat)
		}
	}
	return out
}

func hasNested(tools []Tool) bool {
	for _, t := range tools {
		if len(t.Tools) > 0 {
			return true
		}
	}
	return false
}
