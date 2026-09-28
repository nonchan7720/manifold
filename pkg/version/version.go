package version

import (
	"fmt"
)

var Version = "1.17.0" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
