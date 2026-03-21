package iwa

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/golang/snappy"
)

type ArchiveInfo struct {
	Identifier   uint64
	MessageInfos []MessageInfo
}

type MessageInfo struct {
	Length      uint64
	Type        uint64
	ArchiveData []byte
}

type IWAFile struct {
	ArchiveInfo *ArchiveInfo
	Messages    map[uint64][][]byte
}

func DecompressSnappy(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("data too short")
	}

	first4Bytes := data[:4]
	rest := data[4:]

	firstUint32 := binary.LittleEndian.Uint32(first4Bytes)

	if firstUint32 == 0 || firstUint32 > uint32(len(rest))*2 {
		return snappy.Decode(nil, rest)
	}

	var result []byte
	i := 0

	for i < len(rest) {
		if i+4 > len(rest) {
			break
		}

		chunkType := rest[i]
		chunkLen := binary.LittleEndian.Uint32(rest[i+1 : i+4])
		payload := rest[i+4 : i+4+int(chunkLen)]

		switch chunkType {
		case 0x01:
			compressed, err := snappy.Decode(nil, payload)
			if err != nil {
				return nil, fmt.Errorf("snappy decode error at offset %d: %w", i, err)
			}
			result = append(result, compressed...)
		case 0xff:
			result = append(result, payload...)
		default:
			break
		}

		i += 4 + int(chunkLen)
	}

	if len(result) == 0 {
		return snappy.Decode(nil, rest)
	}

	return result, nil
}

func ReadArchiveInfo(data []byte) (*ArchiveInfo, error) {
	info := &ArchiveInfo{
		MessageInfos: []MessageInfo{},
	}

	r := bytes.NewReader(data)

	for r.Len() > 0 {
		fieldNum, err := ReadVarint(r)
		if err != nil {
			break
		}
		wireType := fieldNum & 0x07
		fieldNum = fieldNum >> 3

		switch {
		case fieldNum == 1 && wireType == 0:
			val, err := ReadVarint(r)
			if err != nil {
				return nil, err
			}
			info.Identifier = val

		case fieldNum == 2 && wireType == 2:
			dataLen, err := ReadVarint(r)
			if err != nil {
				return nil, err
			}
			msgData := make([]byte, dataLen)
			r.Read(msgData)

			mi, err := ParseMessageInfo(msgData)
			if err == nil {
				info.MessageInfos = append(info.MessageInfos, mi)
			}

		case wireType == 6:
			if err := skipGroup(r, fieldNum); err != nil {
				return nil, err
			}

		default:
			if err := SkipWireType(r, wireType); err != nil {
				return nil, err
			}
		}
	}

	return info, nil
}

func ParseMessageInfo(data []byte) (MessageInfo, error) {
	info := MessageInfo{}
	r := bytes.NewReader(data)

	for r.Len() > 0 {
		fieldNum, err := ReadVarint(r)
		if err != nil {
			break
		}
		wireType := fieldNum & 0x07
		fieldNum = fieldNum >> 3

		switch {
		case fieldNum == 1 && wireType == 0:
			val, err := ReadVarint(r)
			if err != nil {
				return info, err
			}
			info.Length = val

		case fieldNum == 2 && wireType == 0:
			val, err := ReadVarint(r)
			if err != nil {
				return info, err
			}
			info.Type = val

		case fieldNum == 3 && wireType == 2:
			dataLen, err := ReadVarint(r)
			if err != nil {
				return info, err
			}
			info.ArchiveData = make([]byte, dataLen)
			r.Read(info.ArchiveData)

		case wireType == 6:
			if err := skipGroup(r, fieldNum); err != nil {
				return info, err
			}

		default:
			if err := SkipWireType(r, wireType); err != nil {
				return info, err
			}
		}
	}

	return info, nil
}

func ReadVarint(r *bytes.Reader) (uint64, error) {
	var val uint64
	var shift uint
	for i := 0; i < 10; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return val, io.EOF
		}
		val |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return val, nil
		}
		shift += 7
	}
	return val, fmt.Errorf("varint overflow")
}

func SkipWireType(r *bytes.Reader, wireType uint64) error {
	switch wireType {
	case 0:
		_, err := r.ReadByte()
		return err
	case 1:
		r.Seek(4, io.SeekCurrent)
		return nil
	case 2:
		len, err := ReadVarint(r)
		if err != nil {
			return err
		}
		r.Seek(int64(len), io.SeekCurrent)
		return nil
	case 5:
		r.Seek(4, io.SeekCurrent)
		return nil
	case 3:
		return nil
	case 6, 7:
		return nil
	}
	return nil
}

func skipGroup(r *bytes.Reader, groupFieldNum uint64) error {
	depth := 1
	for depth > 0 && r.Len() > 0 {
		tag, err := ReadVarint(r)
		if err != nil {
			return err
		}
		wireType := tag & 7
		fieldNum := tag >> 3

		if wireType == 6 && fieldNum == groupFieldNum {
			depth++
		} else if wireType == 7 && fieldNum == groupFieldNum {
			depth--
		} else {
			if err := SkipWireType(r, wireType); err != nil {
				return err
			}
		}
	}
	return nil
}

type ParsedMessage struct {
	TypeID uint64
	Data   []byte
	Fields map[uint64][]byte
}

func ParseMessageData(data []byte) *ParsedMessage {
	msg := &ParsedMessage{
		Fields: make(map[uint64][]byte),
	}
	r := bytes.NewReader(data)

	for r.Len() > 0 {
		fieldNum, err := ReadVarint(r)
		if err != nil {
			break
		}
		wireType := fieldNum & 0x07
		fieldNum = fieldNum >> 3

		switch wireType {
		case 0:
			val, _ := ReadVarint(r)
			msg.Fields[fieldNum] = encodeVarint(val)
		case 2:
			dataLen, _ := ReadVarint(r)
			fieldData := make([]byte, dataLen)
			r.Read(fieldData)
			msg.Fields[fieldNum] = fieldData
		case 5:
			fieldData := make([]byte, 4)
			r.Read(fieldData)
			msg.Fields[fieldNum] = fieldData
		default:
			SkipWireType(r, wireType)
		}
	}

	return msg
}

func encodeVarint(val uint64) []byte {
	var buf []byte
	for val >= 0x80 {
		buf = append(buf, byte(val)|0x80)
		val >>= 7
	}
	buf = append(buf, byte(val))
	return buf
}

func ParseIWAFile(data []byte) (*IWAFile, error) {
	decompressed, err := DecompressSnappy(data)
	if err != nil {
		return nil, fmt.Errorf("decompress error: %w", err)
	}

	info, err := ReadArchiveInfo(decompressed)
	if err != nil {
		return nil, fmt.Errorf("parse ArchiveInfo error: %w", err)
	}

	iwa := &IWAFile{
		ArchiveInfo: info,
		Messages:    make(map[uint64][][]byte),
	}

	for _, mi := range info.MessageInfos {
		if len(mi.ArchiveData) > 0 {
			iwa.Messages[mi.Type] = append(iwa.Messages[mi.Type], mi.ArchiveData)
		}
	}

	return iwa, nil
}

func ReadIWAFromZip(zipPath string, iwaName string) ([]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == iwaName {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("iwa file %s not found in archive", iwaName)
}

func ReadIndexZipFromPages(pagesPath string) ([]byte, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "Index.zip" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("Index.zip not found in pages bundle")
}

func extractFromBundle(pagesPath string, iwaName string) ([]byte, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "Index/"+iwaName {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("%s not found in bundle", iwaName)
}

func ExtractDocumentIWA(pagesPath string) ([]byte, error) {
	if data, err := extractFromBundle(pagesPath, "Document.iwa"); err == nil {
		return data, nil
	}

	indexData, err := ReadIndexZipFromPages(pagesPath)
	if err != nil {
		return nil, err
	}

	r, err := zip.NewReader(bytes.NewReader(indexData), int64(len(indexData)))
	if err != nil {
		return nil, err
	}

	for _, f := range r.File {
		if f.Name == "Document.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("Document.iwa not found")
}

func ExtractAnnotationStorageIWA(pagesPath string) ([]byte, error) {
	if data, err := extractFromBundle(pagesPath, "AnnotationAuthorStorage.iwa"); err == nil {
		return data, nil
	}

	indexData, err := ReadIndexZipFromPages(pagesPath)
	if err != nil {
		return nil, err
	}

	r, err := zip.NewReader(bytes.NewReader(indexData), int64(len(indexData)))
	if err != nil {
		return nil, err
	}

	for _, f := range r.File {
		if f.Name == "AnnotationAuthorStorage.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("AnnotationAuthorStorage.iwa not found")
}

func HasFieldWithValue(data []byte, fieldNum uint64, expectedValue byte) bool {
	parsed := ParseMessageData(data)
	fieldData, ok := parsed.Fields[fieldNum]
	if !ok || len(fieldData) == 0 {
		return false
	}
	return fieldData[0] == expectedValue
}

func HasNonEmptyField(data []byte, fieldNum uint64) bool {
	parsed := ParseMessageData(data)
	fieldData, ok := parsed.Fields[fieldNum]
	if !ok {
		return false
	}
	return len(fieldData) > 0
}
