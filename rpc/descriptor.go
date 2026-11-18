// Package rpc 为 sprout 提供 gRPC 的客户端与服务端能力。
//
// 消息用普通的 Go 结构体定义，结构体标签 pb 声明 protobuf 字段号，
// 框架在运行时把这些结构体反射成真正的 protobuf 描述符，因此使用者既不用
// 编写 .proto 文件，也不用运行 protoc，线格式却与 protoc 生成的产物完全一致。
package rpc

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"
	"unicode"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// tagName 是消息结构体上声明 protobuf 字段号的结构体标签。
const tagName = "pb"

// timestampTypeName 是 time.Time 对应的 well-known 类型全名。
const timestampTypeName = "google.protobuf.Timestamp"

// timestampProtoFile 是 time.Time 对应的 well-known 类型文件路径。
const timestampProtoFile = "google/protobuf/timestamp.proto"

// timeType 是 time.Time 的反射类型，用于识别时间字段。
var timeType = reflect.TypeOf(time.Time{})

// scalarTypes 把 Go 标量类型映射到 protobuf 标量类型。
var scalarTypes = map[reflect.Kind]descriptorpb.FieldDescriptorProto_Type{
	reflect.String:  descriptorpb.FieldDescriptorProto_TYPE_STRING,
	reflect.Bool:    descriptorpb.FieldDescriptorProto_TYPE_BOOL,
	reflect.Int32:   descriptorpb.FieldDescriptorProto_TYPE_INT32,
	reflect.Int64:   descriptorpb.FieldDescriptorProto_TYPE_INT64,
	reflect.Uint32:  descriptorpb.FieldDescriptorProto_TYPE_UINT32,
	reflect.Uint64:  descriptorpb.FieldDescriptorProto_TYPE_UINT64,
	reflect.Float32: descriptorpb.FieldDescriptorProto_TYPE_FLOAT,
	reflect.Float64: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE,
}

// parseTag 解析 pb 标签，返回字段号。标签缺失或非法时返回错误。
func parseTag(tag string) (int32, error) {
	if tag == "" {
		return 0, fmt.Errorf("the %q tag is required on every field", tagName)
	}
	num, err := strconv.Atoi(tag)
	if err != nil {
		return 0, fmt.Errorf("the %q tag %q is not a valid field number", tagName, tag)
	}
	if num <= 0 {
		return 0, fmt.Errorf("the %q tag %q must be a positive field number", tagName, tag)
	}
	return int32(num), nil
}

// jsonName 返回字段的 JSON 名，即字段名的小驼峰形式，与 protoc 的默认规则一致。
func jsonName(name string) string {
	var b []rune
	upperNext := false
	for i, r := range name {
		if r == '_' {
			upperNext = true
			continue
		}
		if i == 0 {
			b = append(b, unicode.ToLower(r))
			continue
		}
		if upperNext {
			b = append(b, unicode.ToUpper(r))
			upperNext = false
			continue
		}
		b = append(b, r)
	}
	return string(b)
}

// derefType 去掉指针，返回底层类型。
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// collectTypes 收集给定类型可达的全部消息类型，结果按类型名排序以保证确定性。
func collectTypes(roots []reflect.Type) ([]reflect.Type, error) {
	seen := map[reflect.Type]bool{}
	var list []reflect.Type
	var walk func(t reflect.Type) error
	walk = func(t reflect.Type) error {
		if seen[t] {
			return nil
		}
		seen[t] = true
		list = append(list, t)
		for i := 0; i < t.NumField(); i++ {
			ft := derefType(t.Field(i).Type)
			switch {
			case ft == timeType:
				continue
			case ft.Kind() == reflect.Map:
				v := derefType(ft.Elem())
				if v.Kind() == reflect.Struct {
					if err := walk(v); err != nil {
						return err
					}
				}
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() != reflect.Uint8:
				e := derefType(ft.Elem())
				if e.Kind() == reflect.Struct {
					if err := walk(e); err != nil {
						return err
					}
				}
			case ft.Kind() == reflect.Struct:
				if err := walk(ft); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, r := range roots {
		if r.Kind() != reflect.Struct {
			return nil, fmt.Errorf("the message type %s must be a struct", r)
		}
		if r.Name() == "" {
			return nil, fmt.Errorf("the message type must be a named struct, an anonymous struct has no message name")
		}
		if err := walk(r); err != nil {
			return nil, err
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	return list, nil
}

// descriptorBuilder 把 Go 结构体类型合成为一个 protobuf 文件描述符。
type descriptorBuilder struct {
	// pkg 是 proto 包名。
	pkg string
	// messages 是合成出来的消息列表。
	messages []*descriptorpb.DescriptorProto
	// needsTimestamp 记录文件是否引用了 Timestamp 这个 well-known 类型。
	needsTimestamp bool
}

// buildFileDescriptor 把给定的 Go 结构体类型合成为一个 protobuf 文件描述符。
// 返回文件描述符，以及「消息全名 → 消息描述符」的映射。
func buildFileDescriptor(pkg, fileName string, roots []reflect.Type) (protoreflect.FileDescriptor, map[string]protoreflect.MessageDescriptor, error) {
	types, err := collectTypes(roots)
	if err != nil {
		return nil, nil, err
	}
	b := &descriptorBuilder{pkg: pkg}
	for _, t := range types {
		dp, err := b.buildMessage(t)
		if err != nil {
			return nil, nil, err
		}
		b.messages = append(b.messages, dp)
	}
	fdp := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(fileName),
		Package:     proto.String(pkg),
		Syntax:      proto.String("proto3"),
		MessageType: b.messages,
	}
	if b.needsTimestamp {
		fdp.Dependency = append(fdp.Dependency, timestampProtoFile)
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot build the file descriptor %s: %w", fileName, err)
	}
	msgs := make(map[string]protoreflect.MessageDescriptor, fd.Messages().Len())
	for i := 0; i < fd.Messages().Len(); i++ {
		md := fd.Messages().Get(i)
		msgs[string(md.FullName())] = md
	}
	return fd, msgs, nil
}

// buildMessage 把一个 Go 结构体类型合成为一个 protobuf 消息描述。
func (b *descriptorBuilder) buildMessage(t reflect.Type) (*descriptorpb.DescriptorProto, error) {
	dp := &descriptorpb.DescriptorProto{Name: proto.String(t.Name())}
	seen := map[int32]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		num, err := parseTag(f.Tag.Get(tagName))
		if err != nil {
			return nil, fmt.Errorf("the message %s: the field %s: %w", t.Name(), f.Name, err)
		}
		if seen[num] {
			return nil, fmt.Errorf("the message %s: the field %s: the field number %d is duplicated", t.Name(), f.Name, num)
		}
		seen[num] = true
		fdp, err := b.buildField(dp, t, f, num)
		if err != nil {
			return nil, fmt.Errorf("the message %s: the field %s: %w", t.Name(), f.Name, err)
		}
		dp.Field = append(dp.Field, fdp)
	}
	return dp, nil
}

// buildField 把一个结构体字段合成为一个 protobuf 字段描述。
func (b *descriptorBuilder) buildField(parent *descriptorpb.DescriptorProto, owner reflect.Type, f reflect.StructField, num int32) (*descriptorpb.FieldDescriptorProto, error) {
	fdp := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(f.Name),
		JsonName: proto.String(jsonName(f.Name)),
		Number:   proto.Int32(num),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	ft := f.Type
	pointer := ft.Kind() == reflect.Pointer
	ft = derefType(ft)

	switch {
	case ft == timeType:
		b.needsTimestamp = true
		fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		fdp.TypeName = proto.String("." + timestampTypeName)
	case ft.Kind() == reflect.Map:
		if err := b.buildMapField(parent, owner.Name(), f.Name, fdp, ft); err != nil {
			return nil, err
		}
	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Uint8:
		fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
	case ft.Kind() == reflect.Slice:
		fdp.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		elem := derefType(ft.Elem())
		if st, ok := scalarTypes[elem.Kind()]; ok {
			fdp.Type = st.Enum()
		} else if elem.Kind() == reflect.Struct {
			fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
			fdp.TypeName = proto.String("." + b.pkg + "." + elem.Name())
		} else {
			return nil, fmt.Errorf("the element type %s of the slice is not supported", elem)
		}
	case ft.Kind() == reflect.Struct:
		fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		fdp.TypeName = proto.String("." + b.pkg + "." + ft.Name())
	default:
		st, ok := scalarTypes[ft.Kind()]
		if !ok {
			return nil, fmt.Errorf("the field type %s is not supported", ft)
		}
		fdp.Type = st.Enum()
	}

	// 指针标量生成 proto3 optional：需要配套一个合成 oneof。
	if pointer && fdp.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		parent.OneofDecl = append(parent.OneofDecl, &descriptorpb.OneofDescriptorProto{
			Name: proto.String("_" + f.Name),
		})
		fdp.Proto3Optional = proto.Bool(true)
		fdp.OneofIndex = proto.Int32(int32(len(parent.OneofDecl) - 1))
	}
	return fdp, nil
}

// buildMapField 为 map 字段合成 map entry 嵌套类型。
func (b *descriptorBuilder) buildMapField(parent *descriptorpb.DescriptorProto, ownerName, fieldName string, fdp *descriptorpb.FieldDescriptorProto, ft reflect.Type) error {
	if ft.Key().Kind() != reflect.String {
		return fmt.Errorf("the map key type %s is not supported, only the string key is supported", ft.Key())
	}
	entryName := fieldName + "Entry"
	valueField := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("value"),
		JsonName: proto.String("value"),
		Number:   proto.Int32(2),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	valueType := derefType(ft.Elem())
	switch {
	case valueType == timeType:
		b.needsTimestamp = true
		valueField.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		valueField.TypeName = proto.String("." + timestampTypeName)
	case valueType.Kind() == reflect.Struct:
		valueField.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		valueField.TypeName = proto.String("." + b.pkg + "." + valueType.Name())
	default:
		st, ok := scalarTypes[valueType.Kind()]
		if !ok {
			return fmt.Errorf("the map value type %s is not supported", valueType)
		}
		valueField.Type = st.Enum()
	}
	entry := &descriptorpb.DescriptorProto{
		Name: proto.String(entryName),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("key"),
				JsonName: proto.String("key"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			},
			valueField,
		},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	}
	parent.NestedType = append(parent.NestedType, entry)
	fdp.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	fdp.TypeName = proto.String("." + b.pkg + "." + ownerName + "." + entryName)
	return nil
}
