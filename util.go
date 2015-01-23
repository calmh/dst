// Copyright 2014 The DST Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package dst

import (
	"math/rand"
	"time"
)

func timestampMicros() timestamp {
	return timestamp(time.Now().UnixNano() / 1000)
}

func randomSeqNo() sequenceNo {
	return sequenceNo(rand.Uint32())
}

func randomConnID() connectionID {
	return connectionID(rand.Uint32() & 0xffffff)
}
