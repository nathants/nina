package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func ValueSlice[T any](val []*T) []T {
	var resp []T
	for _, v := range val {
		resp = append(resp, *v)
	}
	return resp
}


func Ptr[T any](val T) *T {
	return &val
}

func Sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func StructToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	err = json.Unmarshal(b, &m)
	if err != nil {
		panic(err)
	}
	return m
}

func MapToStruct(m map[string]any, out any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func Format(v any) string {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(buf.String())
}

func Pformat(v any) string {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(buf.String())
}

func Last[T any](xs []T) T {
	return xs[len(xs)-1]
}
