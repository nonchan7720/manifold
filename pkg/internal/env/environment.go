package env

import (
	"os"
	"strconv"
)

func envBool(key string) bool {
	v, _ := strconv.ParseBool(os.Getenv(key))
	return v
}

func IsLocal() bool {
	return envBool("LOCAL")
}

func IsCI() bool {
	return envBool("CI")
}

func IsTest() bool {
	return envBool("TEST")
}

func IsLocalOrCIOrTest() bool {
	return IsLocal() || IsCI() || IsTest()
}

func SkipSecureClient() bool {
	return envBool("SKIP_SECURE_CLIENT")
}
