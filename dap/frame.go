package dap

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ReadMessage reads one Content-Length-framed JSON payload from r. The
// header section is ASCII lines terminated by \r\n, followed by an empty
// \r\n, then exactly the byte count declared by `Content-Length`.
//
// Any header other than `Content-Length` is ignored (DAP doesn't currently
// define extra ones but the spec leaves room for it).
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if !strings.HasPrefix(strings.ToLower(line), "content-length:") {
			continue
		}
		v := strings.TrimSpace(line[len("Content-Length:"):])
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("bad Content-Length %q: %w", v, err)
		}
		contentLength = n
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("missing or zero Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

// WriteMessage frames body with a Content-Length header and writes it to w
// as one logical message.
func WriteMessage(w io.Writer, body []byte) error {
	hdr := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, hdr); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}
