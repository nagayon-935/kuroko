package logstore

// Test-only access for the external logstore_test package, which can import
// logger (logger imports logstore, so an in-package test could not).
var (
	CompressStreamWithComment = compressStreamWithComment
	RemoveRotationCandidate   = removeRotationCandidate
)

const AbandonedLogAge = abandonedLogAge
