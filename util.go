package dst

import "time"

func timestampMicros() uint32 {
	return uint32(time.Now().UnixNano() / 1000)
}
