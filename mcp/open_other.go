//go:build !unix

package main

// openNonBlock is not needed where FIFOs cannot be opened through the file system path.
const openNonBlock = 0
