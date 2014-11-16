// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import "time"

func timestampMicros() uint32 {
	return uint32(time.Now().UnixNano() / 1000)
}
