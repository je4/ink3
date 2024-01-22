package server

func emptyIfNil(str *string) string {
	if str == nil {
		return ""
	}
	return *str
}
