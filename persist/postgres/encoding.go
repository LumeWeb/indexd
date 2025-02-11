package postgres

import (
	"bytes"
	"database/sql"
	"fmt"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/wallet"
)

type decodable struct {
	v any
}

// Scan implements the sql.Scanner interface.
func (d *decodable) Scan(src any) error {
	if src == nil {
		return nil // allow null
	}

	switch src := src.(type) {
	case []byte:
		switch v := d.v.(type) {
		case *types.Currency:
			return d.v.(*types.Currency).UnmarshalText(src)
		case types.DecoderFrom:
			dec := types.NewBufDecoder(src)
			v.DecodeFrom(dec)
			return dec.Err()
		case *[]types.Hash256:
			dec := types.NewBufDecoder(src)
			types.DecodeSlice(dec, v)
		case *[]types.Address:
			dec := types.NewBufDecoder(src)
			types.DecodeSlice(dec, v)
		default:
			return fmt.Errorf("cannot scan %T to %T", src, d.v)
		}
		return nil
	case string:
		switch v := d.v.(type) {
		case *types.Currency:
			return v.UnmarshalText([]byte(src))
		default:
			return fmt.Errorf("cannot scan %T to %T", src, d.v)
		}
	case int64:
		switch v := d.v.(type) {
		case *time.Duration:
			*v = time.Duration(src) * time.Millisecond
		default:
			return fmt.Errorf("cannot scan %T to %T", src, d.v)
		}
		return nil
	default:
		return fmt.Errorf("cannot scan %T to %T", src, d.v)
	}
}

func decode(obj any) sql.Scanner {
	return &decodable{obj}
}

func encode(obj any) any {
	switch obj := obj.(type) {
	case time.Duration:
		return time.Duration(obj).Milliseconds()
	case types.Currency:
		return types.Currency(obj).ExactString()
	case []types.Hash256:
		var buf bytes.Buffer
		e := types.NewEncoder(&buf)
		types.EncodeSlice(e, obj)
		e.Flush()
		return buf.Bytes()
	case []types.Address:
		var buf bytes.Buffer
		e := types.NewEncoder(&buf)
		types.EncodeSlice(e, obj)
		e.Flush()
		return buf.Bytes()
	case types.EncoderTo:
		var buf bytes.Buffer
		e := types.NewEncoder(&buf)
		obj.EncodeTo(e)
		e.Flush()
		return buf.Bytes()
	case wallet.Event:
		return encode(&obj)
	default:
		panic(fmt.Sprintf("unsupported type %T", obj))
	}
}
