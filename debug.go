package dst

import (
	"os"
	"strings"
)

var (
	debugConnection = strings.Contains(os.Getenv("DSTDEBUG"), "conn")
	debugMux        = strings.Contains(os.Getenv("DSTDEBUG"), "mux")
)
