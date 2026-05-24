package kiroupstream

import "crypto/rand"

func cryptoRandReadImpl(p []byte) (int, error) { return rand.Read(p) }
