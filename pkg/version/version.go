package version

import (
	"fmt"
)

var Version = "1.22.2" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
