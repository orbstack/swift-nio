package util

func DiffSlices[T comparable](old, new []T) (added, removed []T) {
	oldMap := make(map[T]struct{}, len(old))
	for _, item := range old {
		oldMap[item] = struct{}{}
	}
	newMap := make(map[T]struct{}, len(new))
	for _, item := range new {
		newMap[item] = struct{}{}
	}

	for _, newItem := range new {
		if _, ok := oldMap[newItem]; !ok {
			added = append(added, newItem)
		}
	}
	for _, oldItem := range old {
		if _, ok := newMap[oldItem]; !ok {
			removed = append(removed, oldItem)
		}
	}

	return
}

type Identifiable[I comparable] interface {
	Identifier() I
}

func DiffSlicesKey[I comparable, T Identifiable[I]](old, new []T) (added, removed []T) {
	oldMap := make(map[I]struct{}, len(old))
	for _, item := range old {
		oldMap[item.Identifier()] = struct{}{}
	}
	newMap := make(map[I]struct{}, len(new))
	for _, item := range new {
		newMap[item.Identifier()] = struct{}{}
	}

	for _, newItem := range new {
		if _, ok := oldMap[newItem.Identifier()]; !ok {
			added = append(added, newItem)
		}
	}
	for _, oldItem := range old {
		if _, ok := newMap[oldItem.Identifier()]; !ok {
			removed = append(removed, oldItem)
		}
	}

	return
}
