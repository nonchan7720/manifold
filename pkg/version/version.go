package version

import (
	"fmt"
)

var Version = "1.27.2" // x-release-please-version

var (
	MarkVersion = fmt.Sprintf("v%s", Version)
)
