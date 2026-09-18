package audit

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"time"
)

// encoding/json/v2 has no default representation for time.Duration: it requires
// the field to declare `format:nano` or `format:units`, and types.AuditRecord is
// frozen and declares neither. These options supply one at the call site
// instead.
//
// The wire form is time.Duration's own string — "250ms", "1.5s" — because the
// log is a file a human opens. time.ParseDuration is its exact inverse, so the
// round trip is lossless.
// DurationOptions is the pair, exported because every other package that
// encodes a types.AuditRecord — the Activity endpoint, the CLI — hits the same
// missing representation and must use the same wire form or the log and the API
// will disagree.
func DurationOptions() json.Options {
	return json.JoinOptions(durationMarshaler, durationUnmarshaler)
}

var (
	durationMarshaler = json.WithMarshalers(json.MarshalToFunc(
		func(enc *jsontext.Encoder, d time.Duration) error {
			return enc.WriteToken(jsontext.String(d.String()))
		}))
	durationUnmarshaler = json.WithUnmarshalers(json.UnmarshalFromFunc(
		func(dec *jsontext.Decoder, d *time.Duration) error {
			tok, err := dec.ReadToken()
			if err != nil {
				return err
			}
			switch tok.Kind() {
			case jsontext.KindString:
				v, err := time.ParseDuration(tok.String())
				if err != nil {
					return fmt.Errorf("audit: parse duration: %w", err)
				}
				*d = v
			case jsontext.KindNumber:
				n, err := tok.Int()
				if err != nil {
					return fmt.Errorf("audit: parse duration: %w", err)
				}
				*d = time.Duration(n)
			default:
				return fmt.Errorf("audit: unexpected duration token %v", tok.Kind())
			}
			return nil
		}))
)
