package checker

// IsPrimitive returns true if the name refers to a built-in primitive type.
func IsPrimitive(name string) bool {
	switch name {
	case "string", "int", "float", "bool", "uuid", "timestamp", "json", "file", "secret", "money":
		return true
	}
	return false
}
