package conf

func BuildVariant() string {
	return buildVariant
}

func Release() bool {
	return buildVariant == "release"
}

func Debug() bool {
	return buildVariant == "debug"
}
