package apichat

import "math/rand"

func newRng() *rand.Rand { return rand.New(rand.NewSource(1)) }
