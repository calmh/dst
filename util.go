// Copyright (C) 2014 Jakob Borg and Contributors (see the CONTRIBUTORS file).
// All rights reserved. Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import "time"

func timestampMicros() uint32 {
	return uint32(time.Now().UnixNano() / 1000)
}
