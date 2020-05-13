package types

import (
	"fmt"
	"github.com/pepelazz/projectGenerator/src/utils"
	"strings"
)

const (
	FldTypeString            = "string"
	FldTypeText              = "text"
	FldTypeInt               = "int"
	FldTypeDouble            = "double"
	FldTypeDate              = "date"
	FldTypeJsonb             = "jsonb"
	FldTypeVueComposition    = "vueComposition"
	FldTypeDatetime          = "datetime"
	FldTypeTextArray         = "text[]"
	FldVueTypeSelect         = "select"
	FldVueTypeMultipleSelect = "multipleSelect"
	FldVueTypeTags         	 = "tags"
)

type (
	FldType struct {
		Name   string
		NameRu string
		Type   string
		Vue    FldVue
		Sql    FldSql
		Doc    *DocType // ссылка на сам документ, к которому принадлежит поле
	}

	FldVue struct {
		Name        string
		NameRu      string
		Type        string
		RowCol      [][]int
		Class       []string
		IsRequired  bool
		Readonly    string
		Ext         map[string]string
		Options     []FldVueOptionsItem
		Composition func(ProjectType, DocType) string
	}

	FldSql struct {
		IsSearch    bool
		IsRequired  bool
		Ref         string
		IsUniq      bool
		Size        int
		IsOptionFld bool // признак что поле пишется не в отдельную колонку таблицы, а в json поле options
		Default 	string
	}

	FldVueOptionsItem struct {
		Label string      `json:"label"`
		Value interface{} `json:"value"`
	}
)

func (fld *FldType) PrintPgModel() string {
	typeStr := fmt.Sprintf(`type="%s"`, fld.Type)
	extStr := ""
	if fld.Type == "string" {
		if fld.Sql.Size > 0 {
			typeStr = fmt.Sprintf("type=\"char\",\tsize=%v", fld.Sql.Size)
		} else {
			typeStr = `type="text"`
		}
	}
	if utils.CheckContainsSliceStr(fld.Type, FldTypeDate, FldTypeDatetime) {
		typeStr = `type="timestamp"`
	}
	if fld.Sql.IsRequired {
		extStr = "not null"
	}
	if len(fld.Sql.Default) > 0 {
		extStr = extStr + " default " + fld.Sql.Default
	}
	// ext может быть пустой
	ext := ""
	if len(extStr) > 0 {
		ext = fmt.Sprintf(" \text=\"%s\",", extStr)
	}
	res := fmt.Sprintf("\t{name=\"%s\",\t\t\t\t\t%s,%s\t comment=\"%s\"}", fld.Name, typeStr, ext, fld.NameRu)

	return res
}

func (fld *FldType) PgInsertType() string {
	switch fld.Type {
	case FldTypeDouble:
		return "double precision"
	case FldTypeString:
		return "text"
	case FldTypeDate, FldTypeDatetime:
		return "timestamp"
	default:
		return fld.Type
	}
}

func (fld *FldType) PgUpdateType() string {
	switch fld.Type {
	case FldTypeInt, FldTypeDouble:
		return "number"
	case FldTypeString:
		return "text"
	case FldTypeDate, FldTypeDatetime:
		return "timestamp"
	case FldTypeTextArray:
		return "jsonArrayText"
	default:
		return fld.Type
	}
}

func (fld FldType) SetIsRequired() FldType {
	fld.Sql.IsRequired = true
	fld.Vue.IsRequired = true
	return fld
}

func (fld FldType) SetIsOptionFld() FldType {
	fld.Sql.IsOptionFld = true
	return fld
}
func (fld FldType) SetIsSearch() FldType {
	fld.Sql.IsSearch = true
	return fld
}
func (fld FldType) SetDefault(s string) FldType {
	fld.Sql.Default = s
	return fld
}

func (fld FldType) AddClass(s string) FldType {
	if fld.Vue.Class == nil {
		fld.Vue.Class = []string{}
	}
	fld.Vue.Class = append(fld.Vue.Class, s)
	return fld
}

// передается либо true/false, либо функция вида ()=> item !== 'a'
func (fld FldType) SetReadonly(s string) FldType {
	fld.Vue.Readonly = s
	return fld
}

func (fld FldVue) ClassPrint() string {
	if fld.Class != nil {
		return strings.Join(fld.Class, " ")
	}
	return ""
}

func (fld FldVue) ClassPrintOnlyCol() string {
	if fld.Class != nil {
		arr := []string{}
		for _, cName := range fld.Class {
			if strings.HasPrefix(cName, "col-") {
				arr = append(arr, cName)
			}
		}
		return strings.Join(arr, " ")
	}
	return ""
}
