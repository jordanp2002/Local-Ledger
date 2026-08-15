package contract

import "strings"

const asciiWhitespace = " \t\n\r\v\f"

func TrimASCIIWhitespace(name string) string {
	return strings.Trim(name, asciiWhitespace)
}
