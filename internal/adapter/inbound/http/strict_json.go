package http

import "github.com/gin-gonic/gin/binding"

// init turns on strict JSON decoding for every request body bound through Gin
// (SB-2, SB-4, SB-5). LLD §10.5 requires DisallowUnknownFields: a body that
// contains a field the DTO does not declare is rejected with 400
// validation_failed instead of the field being silently ignored.
func init() {
	binding.EnableDecoderDisallowUnknownFields = true
}
