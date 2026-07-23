package validate

import "fmt"

// The functions below build violation messages in the style of Laravel's
// default validation messages (see Laravel's lang/en/validation.php),
// substituting the field path for Laravel's ":attribute".

func msgRequired(field string) string {
	return fmt.Sprintf("The %s field is required.", field)
}

func msgType(field, want string) string {
	switch want {
	case "string":
		return fmt.Sprintf("The %s must be a string.", field)
	case "number":
		return fmt.Sprintf("The %s must be a number.", field)
	case "integer":
		return fmt.Sprintf("The %s must be an integer.", field)
	case "boolean":
		return fmt.Sprintf("The %s field must be true or false.", field)
	case "array":
		return fmt.Sprintf("The %s must be an array.", field)
	case "object":
		return fmt.Sprintf("The %s must be an object.", field)
	case "null":
		return fmt.Sprintf("The %s field must be null.", field)
	default:
		return fmt.Sprintf("The %s is invalid.", field)
	}
}

func msgEnum(field string) string {
	return fmt.Sprintf("The selected %s is invalid.", field)
}

func msgMinLength(field string, min int) string {
	return fmt.Sprintf("The %s must be at least %d characters.", field, min)
}

func msgMaxLength(field string, max int) string {
	return fmt.Sprintf("The %s must not be greater than %d characters.", field, max)
}

func msgFormat(field string) string {
	return fmt.Sprintf("The %s format is invalid.", field)
}

func msgMin(field string, min float64) string {
	return fmt.Sprintf("The %s must be at least %v.", field, min)
}

func msgMax(field string, max float64) string {
	return fmt.Sprintf("The %s must not be greater than %v.", field, max)
}

func msgProhibited(field string) string {
	return fmt.Sprintf("The %s field is prohibited.", field)
}
