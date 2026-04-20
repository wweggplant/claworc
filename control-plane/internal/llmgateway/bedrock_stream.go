package llmgateway

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// transcodeBedrockEventStream reads the AWS binary event stream from src,
// transcodes each "chunk" event payload (base64-encoded Anthropic SSE chunk)
// into plain SSE lines written to dst, and returns all transcoded bytes for
// subsequent usage parsing.
//
// AWS Event Stream message layout:
//
//	[total-byte-length: uint32 BE]
//	[headers-byte-length: uint32 BE]
//	[prelude-CRC: uint32 BE]        ← we skip CRC verification
//	[headers: variable]
//	[payload: variable]
//	[message-CRC: uint32 BE]        ← we skip CRC verification
func transcodeBedrockEventStream(src io.Reader, dst io.Writer) []byte {
	var captured bytes.Buffer

	for {
		// Prelude: 12 bytes (total-len, header-len, prelude-crc)
		var prelude [12]byte
		if _, err := io.ReadFull(src, prelude[:]); err != nil {
			break // EOF or broken stream
		}

		totalLen := binary.BigEndian.Uint32(prelude[0:4])
		headerLen := binary.BigEndian.Uint32(prelude[4:8])

		if totalLen < 16 || headerLen > totalLen-16 {
			break // guard against malformed frames
		}

		// Body = everything after the 12-byte prelude, minus 4 bytes for message-CRC.
		bodyLen := int(totalLen) - 12
		rest := make([]byte, bodyLen)
		if _, err := io.ReadFull(src, rest); err != nil {
			break
		}

		// payload sits between headers and the final 4-byte message-CRC.
		payloadEnd := bodyLen - 4
		if int(headerLen) > payloadEnd {
			continue
		}
		payload := rest[headerLen:payloadEnd]

		// Bedrock chunk payload: {"bytes":"<base64-encoded Anthropic SSE chunk>"}
		var chunk struct {
			Bytes string `json:"bytes"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil || chunk.Bytes == "" {
			continue
		}

		decoded, err := base64.StdEncoding.DecodeString(chunk.Bytes)
		if err != nil {
			continue
		}

		// Write as an SSE data line.
		line := fmt.Sprintf("data: %s\n\n", decoded)
		io.WriteString(dst, line)    //nolint:errcheck
		captured.WriteString(line)
		if f, ok := dst.(http.Flusher); ok {
			f.Flush()
		}
	}

	return captured.Bytes()
}
