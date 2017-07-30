package gosql

import (
	"bytes"
	"database/sql"
	"github.com/natande/gox"
	"reflect"
	"strings"
	"sync"
)

type sqlExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

type executor struct {
	exe             sqlExecutor
	typeToFieldInfo sync.Map //type:*fieldInfo
}

func (e *executor) getFields(i interface{}) ([]string, []reflect.Value) {
	v := reflect.ValueOf(i)
	if !v.IsValid() {
		panic("invalid")
	}

	for v.Kind() == reflect.Ptr && !v.IsNil() {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		panic("not struct")
	}

	var info *fieldInfo
	if i, ok := e.typeToFieldInfo.Load(v.Type()); ok {
		info = i.(*fieldInfo)
	} else {
		info = getFieldInfo(v.Type())
		e.typeToFieldInfo.Store(v.Type(), info)
	}

	values := make([]reflect.Value, len(info.indexes))
	for i, idx := range info.indexes {
		values[i] = v.Field(idx)
	}

	return info.names, values
}

func (e *executor) getFieldValues(i interface{}) ([]string, []interface{}) {
	columns, fields := e.getFields(i)
	values := make([]interface{}, len(columns))
	for i, f := range fields {
		values[i] = f.Interface()
	}
	return columns, values
}

func (e *executor) Insert(table string, record interface{}) (sql.Result, error) {
	var columns []string
	var values []interface{}
	if m, ok := record.(map[string]interface{}); ok {
		for k, v := range m {
			columns = append(columns, k)
			values = append(values, v)
		}
	} else {
		columns, values = e.getFieldValues(record)
	}

	var buf bytes.Buffer
	buf.WriteString("insert into ")
	buf.WriteString(table)
	buf.WriteString("(")
	buf.WriteString(strings.Join(columns, ","))
	buf.WriteString(") values (")
	buf.WriteString(strings.Repeat("?,", len(columns)))
	buf.Truncate(buf.Len() - 1)
	buf.WriteString(")")
	query := buf.String()
	gox.LogInfo(query, values)
	return e.exe.Exec(query, values...)
}

func (e *executor) Update(table string, record interface{}, where string, args ...interface{}) (sql.Result, error) {
	var columns []string
	var values []interface{}
	if m, ok := record.(map[string]interface{}); ok {
		for k, v := range m {
			columns = append(columns, k)
			values = append(values, v)
		}
	} else {
		columns, values = e.getFieldValues(record)
	}

	var buf bytes.Buffer
	buf.WriteString("update ")
	buf.WriteString(table)
	buf.WriteString(" set ")
	for _, c := range columns {
		buf.WriteString(c)
		buf.WriteString(" = ?,")
	}
	buf.Truncate(buf.Len() - 1)
	if len(where) > 0 {
		buf.WriteString(" where ")
		buf.WriteString(where)
	}
	values = append(values, args...)
	query := buf.String()
	gox.LogInfo(query, values)
	return e.exe.Exec(query, values...)
}

func (e *executor) Upsert(table string, record interface{}) (sql.Result, error) {
	var columns []string
	var values []interface{}
	if m, ok := record.(map[string]interface{}); ok {
		for k, v := range m {
			columns = append(columns, k)
			values = append(values, v)
		}
	} else {
		columns, values = e.getFieldValues(record)
	}

	var buf bytes.Buffer
	buf.WriteString("insert into ")
	buf.WriteString(table)
	buf.WriteString("(")
	buf.WriteString(strings.Join(columns, ","))
	buf.WriteString(") values (")
	buf.WriteString(strings.Repeat("?,", len(columns)))
	buf.Truncate(buf.Len() - 1)
	buf.WriteString(") on duplicate key set ")
	for _, c := range columns {
		buf.WriteString(c)
		buf.WriteString(" = ?,")
	}
	buf.Truncate(buf.Len() - 1)

	values = append(values, values...)
	query := buf.String()
	gox.LogInfo(query, values)
	return e.exe.Exec(query, values...)
}

func (e *executor) Select(table string, records interface{}, where string, args ...interface{}) error {
	v := reflect.ValueOf(records)
	if v.Kind() != reflect.Ptr {
		panic("must be a pointer to slice")
	}

	if v.IsNil() && !v.CanSet() {
		panic("cannot be set value")
	}

	sliceType := v.Type().Elem()
	if sliceType.Kind() != reflect.Slice {
		panic("must be a pointer to slice")
	}

	isPtr := false
	elemType := sliceType.Elem()
	if elemType.Kind() == reflect.Ptr {
		elemType = elemType.Elem()
		isPtr = true
	}

	if elemType.Kind() != reflect.Struct {
		panic("slice element must be a struct or pointer to struct")
	}

	var fi *fieldInfo
	if fv, ok := e.typeToFieldInfo.Load(elemType); ok {
		fi = fv.(*fieldInfo)
	} else {
		fi = getFieldInfo(elemType)
		e.typeToFieldInfo.Store(elemType, fi)
	}

	var buf bytes.Buffer
	buf.WriteString("select ")
	buf.WriteString(strings.Join(fi.names, ","))
	buf.WriteString(" from ")
	buf.WriteString(table)
	if len(where) > 0 {
		buf.WriteString(" where ")
		buf.WriteString(where)
	}
	query := buf.String()
	gox.LogInfo(query, args)
	rows, err := e.exe.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	if v.IsNil() {
		v.Set(reflect.New(sliceType))
	}
	sliceValue := v.Elem()
	for rows.Next() {
		ptrToElem := reflect.New(elemType)
		elem := ptrToElem.Elem()
		fields := make([]interface{}, len(fi.indexes))
		for i, idx := range fi.indexes {
			fields[i] = elem.Field(idx).Addr().Interface()
		}

		err = rows.Scan(fields...)
		if err != nil {
			return err
		}

		if isPtr {
			sliceValue = reflect.Append(sliceValue, ptrToElem)
		} else {
			sliceValue = reflect.Append(sliceValue, elem)
		}
	}
	v.Elem().Set(sliceValue)
	return nil
}

func (e *executor) SelectOne(table string, record interface{}, where string, args ...interface{}) error {
	return nil
}

func (e *executor) Delete(table string, where string, args ...interface{}) (sql.Result, error) {
	var buf bytes.Buffer
	buf.WriteString("delete from ")
	buf.WriteString(table)
	if len(where) > 0 {
		buf.WriteString(" where ")
		buf.WriteString(where)
	}
	query := buf.String()
	gox.LogInfo(query, args)
	return e.exe.Exec(query, args...)
}
