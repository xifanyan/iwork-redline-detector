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
	Records     []*MessageRecord
	ByType      map[uint64][]*MessageRecord
	Messages    map[uint64][][]byte
}

type Field struct {
	Number      uint64
	WireType    uint64
	Raw         []byte
	VarintValue uint64
	HasVarint   bool
	Fixed32     uint32
	HasFixed32  bool
	Fixed64     uint64
	HasFixed64  bool
}

type Message struct {
	Raw        []byte
	Fields     map[uint64][]Field
	FieldOrder []Field
}

type MessageRecord struct {
	ArchiveID uint64
	TypeID    uint64
	Length    uint64
	Raw       []byte
	Parsed    *Message
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

		case wireType == 3:
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

		case wireType == 3:
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
		_, err := ReadVarint(r)
		return err
	case 1:
		r.Seek(8, io.SeekCurrent)
		return nil
	case 2:
		len, err := ReadVarint(r)
		if err != nil {
			return err
		}
		if len > uint64(r.Len()) {
			return fmt.Errorf("skip wiretype 2: length %d exceeds remaining %d", len, r.Len())
		}
		r.Seek(int64(len), io.SeekCurrent)
		return nil
	case 5:
		r.Seek(4, io.SeekCurrent)
		return nil
	case 3, 4:
		return skipGroup(r, wireType)
	}
	return nil
}

func skipGroup(r *bytes.Reader, groupEndTag uint64) error {
	depth := 1
	for depth > 0 && r.Len() > 0 {
		tag, err := ReadVarint(r)
		if err != nil {
			return err
		}
		wireType := tag & 7

		if wireType == 3 {
			depth++
		} else if wireType == 4 {
			depth--
			if depth == 0 {
				return nil
			}
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

func ParseMessage(data []byte) (*Message, error) {
	msg := &Message{
		Raw:    data,
		Fields: make(map[uint64][]Field),
	}

	r := bytes.NewReader(data)
	for r.Len() > 0 {
		tag, err := ReadVarint(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		fieldNum := tag >> 3
		wireType := tag & 0x07
		field := Field{Number: fieldNum, WireType: wireType}

		switch wireType {
		case 0:
			val, err := ReadVarint(r)
			if err != nil {
				return nil, err
			}
			field.VarintValue = val
			field.HasVarint = true
			field.Raw = encodeVarint(val)
		case 1:
			field.Raw = make([]byte, 8)
			if _, err := io.ReadFull(r, field.Raw); err != nil {
				return nil, err
			}
			field.Fixed64 = binary.LittleEndian.Uint64(field.Raw)
			field.HasFixed64 = true
		case 2:
			dataLen, err := ReadVarint(r)
			if err != nil {
				return nil, err
			}
			if dataLen > uint64(r.Len()) {
				return nil, fmt.Errorf("field %d length %d exceeds remaining %d", fieldNum, dataLen, r.Len())
			}
			field.Raw = make([]byte, dataLen)
			if _, err := io.ReadFull(r, field.Raw); err != nil {
				return nil, err
			}
		case 5:
			field.Raw = make([]byte, 4)
			if _, err := io.ReadFull(r, field.Raw); err != nil {
				return nil, err
			}
			field.Fixed32 = binary.LittleEndian.Uint32(field.Raw)
			field.HasFixed32 = true
		default:
			return nil, fmt.Errorf("unsupported wire type %d for field %d", wireType, fieldNum)
		}

		msg.Fields[fieldNum] = append(msg.Fields[fieldNum], field)
		msg.FieldOrder = append(msg.FieldOrder, field)
	}

	return msg, nil
}

func (m *Message) FieldsByNumber(n uint64) []Field {
	if m == nil {
		return nil
	}
	return m.Fields[n]
}

func (m *Message) FirstField(n uint64) (Field, bool) {
	fields := m.FieldsByNumber(n)
	if len(fields) == 0 {
		return Field{}, false
	}
	return fields[0], true
}

func (m *Message) FirstVarint(n uint64) (uint64, bool) {
	field, ok := m.FirstField(n)
	if !ok {
		return 0, false
	}
	return field.AsVarint()
}

func (m *Message) NestedMessages(n uint64) []*Message {
	fields := m.FieldsByNumber(n)
	if len(fields) == 0 {
		return nil
	}

	nested := make([]*Message, 0, len(fields))
	for _, field := range fields {
		child, err := field.AsMessage()
		if err == nil {
			nested = append(nested, child)
		}
	}
	return nested
}

func (m *Message) Walk(fn func(path []uint64, msg *Message) bool) {
	if m == nil || fn == nil {
		return
	}
	var walk func(path []uint64, current *Message)
	walk = func(path []uint64, current *Message) {
		if !fn(path, current) {
			return
		}
		for _, field := range current.FieldOrder {
			child, err := field.AsMessage()
			if err != nil {
				continue
			}
			nextPath := append(append([]uint64{}, path...), field.Number)
			walk(nextPath, child)
		}
	}
	walk(nil, m)
}

func (f Field) AsVarint() (uint64, bool) {
	if !f.HasVarint {
		return 0, false
	}
	return f.VarintValue, true
}

func (f Field) AsBool() (bool, bool) {
	val, ok := f.AsVarint()
	if !ok {
		return false, false
	}
	if val > 1 {
		return false, false
	}
	return val == 1, true
}

func (f Field) AsBytes() ([]byte, bool) {
	if f.WireType != 2 {
		return nil, false
	}
	return f.Raw, true
}

func (f Field) AsMessage() (*Message, error) {
	if f.WireType != 2 {
		return nil, fmt.Errorf("field %d is not length-delimited", f.Number)
	}
	if len(f.Raw) == 0 {
		return nil, fmt.Errorf("field %d is empty", f.Number)
	}
	return ParseMessage(f.Raw)
}

func ParseMessageData(data []byte) *ParsedMessage {
	legacy := &ParsedMessage{
		Data:   data,
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
			val, err := ReadVarint(r)
			if err != nil {
				return legacy
			}
			legacy.Fields[fieldNum] = encodeVarint(val)
		case 2:
			dataLen, err := ReadVarint(r)
			if err != nil {
				return legacy
			}
			if dataLen > uint64(r.Len()) {
				return legacy
			}
			fieldData := make([]byte, dataLen)
			if _, err := io.ReadFull(r, fieldData); err != nil {
				return legacy
			}
			legacy.Fields[fieldNum] = fieldData
		case 5:
			fieldData := make([]byte, 4)
			if _, err := io.ReadFull(r, fieldData); err != nil {
				return legacy
			}
			legacy.Fields[fieldNum] = fieldData
		default:
			if err := SkipWireType(r, wireType); err != nil {
				return legacy
			}
		}
	}

	return legacy
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
		ByType:      make(map[uint64][]*MessageRecord),
		Messages:    make(map[uint64][][]byte),
	}

	for _, mi := range info.MessageInfos {
		if len(mi.ArchiveData) > 0 {
			record := &MessageRecord{
				ArchiveID: info.Identifier,
				TypeID:    mi.Type,
				Length:    mi.Length,
				Raw:       mi.ArchiveData,
			}
			if parsed, err := ParseMessage(mi.ArchiveData); err == nil {
				record.Parsed = parsed
			}
			iwa.Records = append(iwa.Records, record)
			iwa.ByType[mi.Type] = append(iwa.ByType[mi.Type], record)
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
		if f.Name == "Document.iwa" || f.Name == "index/Document.iwa" || f.Name == "Index/Document.iwa" {
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
