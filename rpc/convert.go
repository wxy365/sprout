package rpc

import (
	"fmt"
	"reflect"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newDynamicMessage 为给定的消息描述符创建一个空的动态消息。
func newDynamicMessage(md protoreflect.MessageDescriptor) *dynamicpb.Message {
	return dynamicpb.NewMessage(md)
}

// toMessage 把 Go 结构体的值写入 protobuf 动态消息。
func toMessage(v reflect.Value, msg protoreflect.Message) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		num, err := parseTag(f.Tag.Get(tagName))
		if err != nil {
			return fmt.Errorf("the field %s: %w", f.Name, err)
		}
		fd := msg.Descriptor().Fields().ByNumber(protoreflect.FieldNumber(num))
		if fd == nil {
			return fmt.Errorf("the field %s is not in the descriptor", f.Name)
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		if err := setField(fd, fv, msg); err != nil {
			return fmt.Errorf("the field %s: %w", f.Name, err)
		}
	}
	return nil
}

// setField 把一个 Go 值写入消息的对应字段。
func setField(fd protoreflect.FieldDescriptor, fv reflect.Value, msg protoreflect.Message) error {
	switch {
	case fd.IsMap():
		if fv.IsNil() {
			return nil
		}
		mv := msg.Mutable(fd).Map()
		iter := fv.MapRange()
		for iter.Next() {
			key := reflect.ValueOf(iter.Key().String()).Convert(fv.Type().Key())
			vv, err := singleValue(fd.MapValue(), iter.Value())
			if err != nil {
				return err
			}
			mv.Set(protoreflect.ValueOfString(key.String()).MapKey(), vv)
		}
		return nil
	case fd.IsList():
		lv := msg.Mutable(fd).List()
		for i := 0; i < fv.Len(); i++ {
			ev := fv.Index(i)
			if ev.Kind() == reflect.Pointer {
				if ev.IsNil() {
					continue
				}
				ev = ev.Elem()
			}
			pv, err := singleValue(fd, ev)
			if err != nil {
				return err
			}
			lv.Append(pv)
		}
		return nil
	default:
		pv, err := singleValue(fd, fv)
		if err != nil {
			return err
		}
		msg.Set(fd, pv)
		return nil
	}
}

// singleValue 把一个 Go 值转成 protobuf 的字段值。
func singleValue(fd protoreflect.FieldDescriptor, v reflect.Value) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		if string(fd.Message().FullName()) == timestampTypeName {
			t, ok := v.Interface().(time.Time)
			if !ok {
				return protoreflect.Value{}, fmt.Errorf("the timestamp field expects a time.Time")
			}
			ts := timestamppb.New(t)
			sub := newDynamicMessage(fd.Message())
			sub.Set(fd.Message().Fields().ByNumber(1), protoreflect.ValueOfInt64(ts.Seconds))
			sub.Set(fd.Message().Fields().ByNumber(2), protoreflect.ValueOfInt32(ts.Nanos))
			return protoreflect.ValueOfMessage(sub), nil
		}
		sub := newDynamicMessage(fd.Message())
		if err := toMessage(v, sub); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfMessage(sub), nil
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(v.String()), nil
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(v.Bool()), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(v.Int())), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(v.Int()), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(v.Uint())), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(v.Uint()), nil
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(float32(v.Float())), nil
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(v.Float()), nil
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes(v.Bytes()), nil
	}
	return protoreflect.Value{}, fmt.Errorf("the field kind %s is not supported", fd.Kind())
}

// fromMessage 把 protobuf 动态消息读入 Go 结构体。
func fromMessage(msg protoreflect.Message, out reflect.Value) error {
	t := out.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		num, err := parseTag(f.Tag.Get(tagName))
		if err != nil {
			return fmt.Errorf("the field %s: %w", f.Name, err)
		}
		fd := msg.Descriptor().Fields().ByNumber(protoreflect.FieldNumber(num))
		if fd == nil {
			continue
		}
		if !msg.Has(fd) {
			continue
		}
		if err := loadField(fd, msg.Get(fd), out.Field(i)); err != nil {
			return fmt.Errorf("the field %s: %w", f.Name, err)
		}
	}
	return nil
}

// loadField 把一个 protobuf 字段值读入 Go 的对应字段。
func loadField(fd protoreflect.FieldDescriptor, pv protoreflect.Value, fv reflect.Value) error {
	switch {
	case fd.IsMap():
		if fv.IsNil() {
			fv.Set(reflect.MakeMap(fv.Type()))
		}
		pv.Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			fv.SetMapIndex(
				reflect.ValueOf(k.String()).Convert(fv.Type().Key()),
				reflect.ValueOf(v.Interface()).Convert(fv.Type().Elem()),
			)
			return true
		})
		return nil
	case fd.IsList():
		lv := pv.List()
		slice := reflect.MakeSlice(fv.Type(), 0, lv.Len())
		for i := 0; i < lv.Len(); i++ {
			elem := reflect.New(fv.Type().Elem()).Elem()
			if err := loadSingle(fd, lv.Get(i), elem); err != nil {
				return err
			}
			slice = reflect.Append(slice, elem)
		}
		fv.Set(slice)
		return nil
	default:
		return loadSingle(fd, pv, fv)
	}
}

// loadSingle 把一个 protobuf 字段值读入一个 Go 值。
func loadSingle(fd protoreflect.FieldDescriptor, pv protoreflect.Value, fv reflect.Value) error {
	if fd.Kind() == protoreflect.MessageKind {
		if string(fd.Message().FullName()) == timestampTypeName {
			fields := fd.Message().Fields()
			sec := pv.Message().Get(fields.ByNumber(1)).Int()
			nanos := pv.Message().Get(fields.ByNumber(2)).Int()
			t := time.Unix(sec, nanos).UTC()
			if fv.Kind() == reflect.Pointer {
				p := reflect.New(fv.Type().Elem())
				p.Elem().Set(reflect.ValueOf(t))
				fv.Set(p)
				return nil
			}
			fv.Set(reflect.ValueOf(t))
			return nil
		}
		target := fv
		if fv.Kind() == reflect.Pointer {
			target = reflect.New(fv.Type().Elem()).Elem()
		}
		if err := fromMessage(pv.Message(), target); err != nil {
			return err
		}
		if fv.Kind() == reflect.Pointer {
			p := reflect.New(fv.Type().Elem())
			p.Elem().Set(target)
			fv.Set(p)
			return nil
		}
		fv.Set(target)
		return nil
	}
	fv.Set(reflect.ValueOf(pv.Interface()).Convert(fv.Type()))
	return nil
}
