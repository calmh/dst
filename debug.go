// Copyright (C) 2014 Jakob Borg and Contributors (see the CONTRIBUTORS file).
// All rights reserved. Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"os"
	"strings"
)

var (
	debugConnection = strings.Contains(os.Getenv("DSTDEBUG"), "conn")
	debugMux        = strings.Contains(os.Getenv("DSTDEBUG"), "mux")
)
