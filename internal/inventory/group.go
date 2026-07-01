package inventory

// groupBy buckets items by key(item). An untagged resource's empty-string
// key groups together under "" -- rendering that bucket as "unknown" is
// the display layer's job (see DECISIONS.md, "Introduce a light
// Project/Environment tagging convention").
func groupBy[T any](items []T, key func(T) string) map[string][]T {
	groups := make(map[string][]T)
	for _, item := range items {
		k := key(item)
		groups[k] = append(groups[k], item)
	}
	return groups
}

// GroupInstancesByProject groups instances by their Project tag.
func GroupInstancesByProject(instances []Instance) map[string][]Instance {
	return groupBy(instances, func(i Instance) string { return i.Project })
}

// GroupInstancesByEnvironment groups instances by their Environment tag.
func GroupInstancesByEnvironment(instances []Instance) map[string][]Instance {
	return groupBy(instances, func(i Instance) string { return i.Environment })
}

// GroupImagesByProject groups images by their Project tag.
func GroupImagesByProject(images []Image) map[string][]Image {
	return groupBy(images, func(i Image) string { return i.Project })
}

// GroupImagesByEnvironment groups images by their Environment tag.
func GroupImagesByEnvironment(images []Image) map[string][]Image {
	return groupBy(images, func(i Image) string { return i.Environment })
}
