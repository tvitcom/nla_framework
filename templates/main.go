package templates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iancoleman/strcase"
	"github.com/tvitcom/nla_framework/types"
	"github.com/tvitcom/nla_framework/utils"
	"io/ioutil"
	"log"
	"os"
	"path"
	"runtime"
	"strings"
	"text/template"
)

var project *types.ProjectType

func SetProject(p *types.ProjectType) {
	project = p
}

var funcMap = template.FuncMap{
	"ToUpper":             strings.ToUpper,
	"ToLower":             strings.ToLower,
	"UpperCaseFirst":      utils.UpperCaseFirst,
	"ToLowerCamel":        strcase.ToLowerCamel,
	"ToCamel":        	   strcase.ToCamel,
	"PrintVueFldTemplate": PrintVueFldTemplate,
	"ArrayStringJoin": arrayStringJoin,
	"GetPgTimeZone": func() string {
		if project !=nil {
			return project.Config.Postgres.TimeZone
		}
		return "null"
	},
	"PrintFldSelectOptions": PrintFldSelectOptions,
}

func ParseTemplates(p types.ProjectType) map[string]*template.Template {

	// парсинг общих шаблонов
	res := map[string]*template.Template{}

	// функция которая: файлы с шаблонами -> map[string]*template.Template
	readFiles := func(prefix, delimLeft, delimRight string, path ...string) {
		tmpls, err := template.New("").Funcs(funcMap).Delims(delimLeft, delimRight).ParseFiles(path...)
		utils.CheckErr(err, "ParseFiles")
		for _, t := range tmpls.Templates() {
			res[prefix+t.Name()] = t
		}
	}

	currentDir := getCurrentDir()

	// project
	path := currentDir + "/project/"
	readFiles("project_", "{{", "}}", path+"config.toml", path+"main.go", path+"docker-compose.yml", path+"docker-compose.dev.yml", path+"restoreDump.sh", path+"deploy.ps1", path+"Dockerfile")
	if p.IsBackupOnYandexDisk() {
		readFiles("project_", "[[", "]]", path+"deployYandexBackup.ps1")
	}
	// webClient
	path = fmt.Sprintf("%s/webClient/quasar_%v/doc/", currentDir, p.GetQuasarVersion())
	readFiles("webClient_", "[[", "]]", path+"index.vue", path+"item.vue", path+"itemWithTabs.vue", path+"tabInfo.vue")
	if p.GetQuasarVersion() == 1 {
		readFiles("webClient_", "[[", "]]", path+"tabTasks.vue")
	}

	// sql
	path = currentDir + "/sql/"
	readFiles("sql_", "{{", "}}", path+"main.toml")
	path = currentDir + "/sql/function/"
	readFiles("sql_function_", "{{", "}}", path+"get_by_id.sql", path+"list.sql", path+"update.sql", path+"trigger_before.sql", path+"trigger_after.sql")
	readFiles("sql_function_", "[[", "]]", path+"create.sql")
	// отдельно читаем шаблон action для stateMachine. Там нужно передавать свой map с параметрами
	res["sql_function_action.sql"] = stateMachineReadTmplAction(funcMap, path+"action.sql")

	// парсинг шаблонов для конкретного документа
	for i, d := range p.Docs {
		for tName, dt := range d.Templates {
			// возможность расширить функции для шаблона.
			// Если в документе определена FuncMap, то расширяем ее стандартными функциями FuncMap и передаем в шаблон
			fMap := funcMap
			if dt.FuncMap != nil {
				fMap = dt.FuncMap
				for k, v := range funcMap {
					fMap[k] = v
				}
			}
			// извлекаем имя файла шаблона, чтобы использовать его в качестве имени шабона. Иначе могут быть ошибки
			path := strings.Split(dt.Source, "/")
			fName := path[len(path)-1]
			t, err := template.New(fName).Funcs(fMap).Delims("[[", "]]").ParseFiles(dt.Source)
			utils.CheckErr(err, fmt.Sprintf("ParseTemplates doc: %s tmpl: %s parse template error: %s", d.Name, tName, err))
			// сохраняем template в поле структуры
			dt.Tmpl = t
		}
		// дописываем стандартные шаблоны
		baseTmplNames := []string{}
		if d.IsBaseTemplates.Vue {
			baseTmplNames = append(baseTmplNames, "webClient_item.vue", "webClient_index.vue")
		}

		if d.IsBaseTemplates.Sql {
			baseTmplNames = append(baseTmplNames, "sql_main.toml", "sql_function_get_by_id.sql", "sql_function_list.sql", "sql_function_update.sql", "sql_function_trigger_before.sql", "sql_function_trigger_after.sql")
		}
		if d.StateMachine != nil {
			baseTmplNames = append(baseTmplNames, "sql_function_action.sql", "sql_function_create.sql")
		}
		// если документ отмечен свойством рекурсии, то дополнительные шаблоны
		if d.IsRecursion {
			docIsRecursionProccess(p, &d)
		}
		// если есть интеграции, то создаем дополнительные шаблоны
		docIsIntegrationProccess(p, &d)

		// в случае если указаны табы, то подбираем соответствующие шаблоны
		for _, tab := range d.Vue.Tabs {
			var t *template.Template
			// ищем в списке общих шаблонов
			if t1, ok := res["webClient_"+tab.TmplName]; ok {
				t = t1
			}
			// потом ищем в шаблонах конкретного документа
			if t1, ok := d.Templates["webClient_"+tab.TmplName]; ok {
				t = t1.Tmpl
			}
			if t == nil {
				log.Fatalf("ParseTemplates: Template not found for tab %s webClient_%s", d.Name, tab.TmplName)
			}

			tName := "webClient_tabs_" + tab.Title
			compPath := d.Name
			if len(d.Vue.Path) > 0 {
				compPath = d.Vue.Path // в случае если указан специальный путь к компоненте
			}
			distPath := fmt.Sprintf("%s/webClient/src/app/components/%s/tabs/%s", p.DistPath, compPath, tab.Title)
			d.Templates[tName] = &types.DocTemplate{Tmpl: t, DistPath: distPath, DistFilename: "index.vue"}
		}

		for _, fld := range d.Flds {
			// если в документе есть поле с типом тэг, то создаем sql метод для запроса списка тэгов.
			fldTagProccess(p, &d, &fld)
			// если в документе есть поле с типо jsonList, то создаем специальную компоненту
			fldJsonListProccess(p, &d, &fld)
		}

		for _, tName := range baseTmplNames {
			// если шаблона с таким именем нет, то добавляем стандартный
			if _, ok := d.Templates[tName]; !ok {
				if tName == "sql_function_trigger_before.sql" && !d.Sql.IsBeforeTrigger {
					continue
				}
				if tName == "sql_function_trigger_after.sql" && !d.Sql.IsAfterTrigger {
					continue
				}
				params := map[string]string{}
				if len(d.Vue.Path)> 0 {
					params["doc.Vue.Path"] = d.Vue.Path
				}

				distPath, distFilename := utils.ParseDocTemplateFilename(d.Name, tName, p.DistPath, i, params)
				tmpl := res[tName]

				// возможность переопределить шаблон
				// если указаны табы, то подменяем шаблон item.vue на itemWithTabs.vue
				if len(d.Vue.Tabs) > 0 {
					if strings.HasPrefix(distPath, "../src/webClient/src/app/components") && distFilename == "item.vue" {
						tmpl = res["webClient_itemWithTabs.vue"]
					}
				}
				// игнорируем шаблоны для табов, их добавляем по специальным путям, которые указаны в d.Vue.Tabs (см раздел выше)
				if strings.HasPrefix(tName, "webClient_tab") {
					continue
				}
				d.Templates[tName] = &types.DocTemplate{Tmpl: tmpl, DistPath: distPath, DistFilename: distFilename}

				// в случае state machine переопределяем шаблон update
				if d.IsStateMachine() {
					if tName == "sql_function_update.sql" {
						if _, ok := d.Templates["sql_function_update.sql"]; ok {
							d.Templates["sql_function_update.sql"].Tmpl = stateMachineReadTmplUpdate(funcMap,  currentDir + "/sql/function/stateMachine_update.sql")
						}
					}
					if tName == "webClient_item.vue" && p.GetQuasarVersion() == 1 {
						if _, ok := d.Templates["webClient_item.vue"]; ok {
							d.Templates["webClient_item.vue"].Tmpl = stateMachineReadTmplWebclientItem(funcMap, currentDir + "/webClient/quasar_1/doc/comp/stateMachine/webClient_item.vue")
						}
					}
				}
			}
		}

		// если state machine то добавляем в sql методы
		if d.IsStateMachine() {
			if d.Sql.Methods == nil {
				d.Sql.Methods = map[string]*types.DocSqlMethod{}
			}
			if _, ok := d.Sql.Methods[d.Name+"_create"]; !ok {
				d.Sql.Methods[d.Name+"_create"] = &types.DocSqlMethod{Name: d.Name+"_create"}
			}
			if _, ok := d.Sql.Methods[d.Name+"_action"]; !ok {
				d.Sql.Methods[d.Name+"_action"] = &types.DocSqlMethod{Name: d.Name+"_action"}
			}
		}

		p.Docs[i] = d
	}

	return res
}

func ExecuteToFile(t *template.Template, d interface{}, path, filename string) error {
	if t == nil {
		log.Fatalf("template is nil for path '%s/%s'\n", path, filename)
	}
	err := os.MkdirAll(path, os.ModePerm)
	if err != nil {
		return err
	}
	var tpl bytes.Buffer
	err = t.Execute(&tpl, d)
	if err != nil {
		return err
	}
	// для оптимизации записи файлов webClient (чтобы ускорить рестарт quasar), проверяем что файл изменен и только в этом случае его перезаписываем
	if strings.Contains(path, "webClient") {
		if existFile, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", path, filename)); err == nil {
			isEqual := utils.ByteSliceEqual(existFile, []byte(tpl.String()))
			if isEqual {
				return nil
			}
			//fmt.Printf("file changed: %s/%s not equal\n", path, filename)
		}
	}
	return ioutil.WriteFile(path+"/"+filename, []byte(tpl.String()), 0644)
}

// печать vue темплейтов для
func PrintVueFldTemplate(fld types.FldType) string {
	name := fld.Vue.Name
	if len(name) == 0 {
		name = fld.Name
	}
	nameRu := fld.Vue.NameRu
	if len(nameRu) == 0 {
		nameRu = fld.NameRu
	}
	readonly := fld.Vue.Readonly
	if len(readonly) == 0 {
		readonly = "false"
	}
	fldType := fld.Vue.Type
	if len(fldType) == 0 {
		fldType = fld.Type
		// в случае ref поля
		if fld.Type == types.FldTypeInt && len(fld.Sql.Ref) > 0 {
			fldType = "ref"
		}
	}
	borderStyle := "outlined"
	if fld.Vue.IsBorderless {
		borderStyle = "borderless"
	}
	// если указана функция для композиции, то меняем тип на vueComposition
	if fld.Vue.Composition != nil {
		fldType = types.FldTypeVueComposition
	}
	if fld.Vue.Type == types.FldVueTypeTags {
		fldType = types.FldVueTypeTags
	}
	params := ""
	if len(fld.Vue.Class) > 0 {
		params = params + fmt.Sprintf(" class='q-mb-sm %s' ", fld.Vue.ClassPrint())
	} else {
		params = params + " class='q-mb-sm' "
	}
	if len(fld.Vue.Vif)>0 {
		params = params + fmt.Sprintf(" v-if=\"%s\" ", fld.Vue.Vif)
	}
	// не во всех случаях fld.Doc доступен, но там и label не нужен. Например doc.AddFld(t.GetFldVueCompositionRefList(&doc, t.VueCompRefListWidgetParams{...
	var labelI18n string
	if fld.Doc != nil {
		labelI18n = fld.Doc.Name + "." + name
	}
	switch fldType {
	case types.FldTypeString, types.FldTypeText, types.FldTypeUuid:
		autogrow := "autogrow"
		// для коротких текстов не делаем autogrow
		if fld.Sql.Size > 0 && fld.Sql.Size < 40 {
			autogrow = ""
		}
		return fmt.Sprintf(`<q-input %s type='text' v-model="item.%s" :label="$t('%s')" %s :readonly='%s' %s/>`, borderStyle, name, labelI18n, autogrow, readonly, params)
	case types.FldTypeInt, types.FldTypeInt64, types.FldTypeDouble:
		return fmt.Sprintf(`<q-input %s type='number' v-model="item.%s" :label="$t('%s')" :readonly='%s' %s/>`, borderStyle, name, labelI18n, readonly, params)
	// дата
	case types.FldTypeDate:
		return fmt.Sprintf(`<comp-fld-date %s :label="$t('%s')" :date-string="$utils.formatPgDate(item.%s)" @update="v=> item.%s = v" :readonly='%s' %s/>`, borderStyle, labelI18n, name, name, readonly, params)
	// дата с временем
	case types.FldTypeDatetime:
		return fmt.Sprintf(`<comp-fld-date-time %s :label="$t('%s')" :date-string="$utils.formatPgDateTime(item.%s)" @update="v=> item.%s = v" @clear="item.%s=null" :readonly='%s' %s/>`, borderStyle, labelI18n, name, name, name, readonly, params)
	case types.FldVueTypePhone:
		return fmt.Sprintf(`<q-input %s mask="+# (###) ### - ####" v-model="item.%s" :label="$t('%s')" :readonly='%s' %s><template v-slot:prepend><q-icon name="phone"/></template></q-input>`, borderStyle, name, labelI18n, readonly, params)
	case types.FldVueTypeEmail:
		return fmt.Sprintf(`<q-input %s type='email' v-model="item.%s" :label="$t('%s')" :readonly='%s' %s><template v-slot:prepend><q-icon name="email"/></template></q-input>`, borderStyle, name, labelI18n, readonly, params)
	// вариант ссылки на другую таблицу
	case "ref":
		// если map Ext не инициализирован, то создаем его, чтобы не было ошибки при json.Marshal
		if fld.Vue.Ext == nil {
			fld.Vue.Ext = map[string]string{}
		}
		// если специально не определено поле для ajaxSelectTitle, то формируем стандартное [ref_table_name]_title
		ajaxSelectTitle := strings.TrimSuffix(fld.Name, "_id") + "_title"
		if v, ok := fld.Vue.Ext["ajaxSelectTitle"]; ok {
			ajaxSelectTitle = v
		}
		// если есть параметр rawJsonExt, то используем его для ext. Остальные параметры игнорируются
		var extJsonStr []byte
		if rawJson, ok := fld.Vue.Ext["rawJsonExt"]; ok {
			// если есть доп параметры, то вручную дописываем их к json строке
			if len(fld.Vue.Ext)>0{
				rawJson = strings.TrimSuffix(rawJson, "}")
				for k, v := range fld.Vue.Ext {
					if k != "rawJsonExt" {
 						rawJson = fmt.Sprintf(`%s, %s: "%s"`, rawJson, k, v)
					}
				}
				rawJson = rawJson + "}"
			}
			extJsonStr = []byte(rawJson)
		} else {
			var err error
			extJsonStr, err = json.Marshal(fld.Vue.Ext)
			utils.CheckErr(err, fmt.Sprintf("json.Marshal(fld.Vue.Ext) fld %s", fld.Name))
		}

		// заполняем название postgres метода для получения списка. По дефолту [ref_table_name]_list
		pgMethod := fld.Sql.Ref + "_list"
		if m, ok := fld.Vue.Ext["pgMethod"]; ok {
			pgMethod = m
		}
		return fmt.Sprintf(`<comp-fld-ref-search %s pgMethod="%s" :label="$t('%s')" :item='item.%s' :itemId='item.%s' :ext='%s' @update="v=> item.%s = v.id" @clear="item.%s = null" :readonly='%s' %s/>`, borderStyle, pgMethod, labelI18n, ajaxSelectTitle, name, extJsonStr, name, name, readonly, params)
	case types.FldVueTypeSelect, types.FldVueTypeMultipleSelect:
		options, err := json.Marshal(fld.Vue.Options)
		utils.CheckErr(err, fmt.Sprintf("'%s' json.Marshal(fld.Vue.Options)", fld.Name))
		multiple := ""
		if fldType == types.FldVueTypeMultipleSelect {
			multiple = "multiple"
		}
		isClearable := ""
		for key, _ := range fld.Vue.Ext {
			if key == "isClearable" {
				isClearable = "clearable"
			}
		}
		return fmt.Sprintf(`<q-select %s :label="$t('%s')" v-model='item.%s' :options='%s' %s %s :readonly='%s' %s/>`, borderStyle, labelI18n, name, options, multiple, isClearable, readonly, params)
	case types.FldTypeVueComposition:
		if fld.Vue.Composition == nil {
			log.Fatal(fmt.Sprintf("fld have type '%s', but fld.Vue.Composition function is nil", types.FldTypeVueComposition))
		}
		// возможен вариант что функция рендеринга поля шаблона вызываается до того как сам документ был инициализирован и соответственно была заполнена ссылка на него в поле fld.Doc
		// в таком случае в функуию передаем пустой документ. Если функция не использует ссылку на документ, то все ок. Но если в функции идет обращение к инфе о документе, то функция отработает некорректно.
		// возможное решение, чтобы вызов функции в шаблоне происходил уже после инициализации документа
		linkOnDoc := types.DocType{}
		if fld.Doc != nil {
			linkOnDoc = *fld.Doc
		}
		return fld.Vue.Composition(*project, linkOnDoc, fld)
	case types.FldVueTypeTags:
		if fld.Vue.Ext["onlyExistTags"] == "true" {
			// вариант когда нельзя создавать новые тэги, только выбирать из существующих
			return fmt.Sprintf(`<q-select %s :label="$t('%s')" v-model='item.%s' use-chips multiple @filter='%[3]sFilterFn' :options='%[3]sFilterOptions' :readonly='%s'/>`, borderStyle, labelI18n, name, readonly, params)
		} else {
			// вариант когда можно создавать новые тэги
			return fmt.Sprintf(`<q-select %s :label="$t('%s')" v-model='item.%s' use-input use-chips multiple input-debounce='0' @new-value='%[3]sCreateValue' @filter='%[3]sFilterFn' :options='%[3]sFilterOptions' :readonly='%s'/>`, borderStyle, labelI18n, name, readonly, params)
		}
	case types.FldVueTypeCheckbox:
		return fmt.Sprintf(`<q-checkbox :label="$t('%s')" v-model='item.%s' :disable='%s' :false-value='false' indeterminate-value='some' %s/>`, labelI18n, name, readonly, params)
	case types.FldVueTypeRadio:
		options := ""
		for _, v := range fld.Vue.Options {
			options = fmt.Sprintf(`%s <q-radio size='xs' dense v-model='item.%s' val='%s' label='%s' :disable ='%s'/>\n`, options, name, v.Value, v.Label, readonly)
		}
		return fmt.Sprintf(`<div class="row q-col-gutter-md q-pb-xs q-mb-sm">
	<div class="col-4">{{$t('%s')}}</div>
	<div class="col-8 q-gutter-sm ">
	%s
	</div>
	</div>
`, labelI18n, options)
	case types.FldVueTypeDadataAddress:
		return fmt.Sprintf(`<comp-fld-address %s :label="$t('%s')" :fld='item.%s' @update="v=> item.%s = v" :readonly='%s' %s/>`, borderStyle, labelI18n, name, name, readonly, params)
	case types.FldVueTypeJsonList:
		return fmt.Sprintf(`<comp-fld-json-list-%s :label="$t('%s')" :item='item' :fld='item.%s' @update='item.%s = $event' :readonly='%s' %s/>`, name, labelI18n, name, name, readonly, params)
	case types.FldVueTypeFiles:
		extStr := ""
		for k, v := range fld.Vue.Ext {
			extStr = extStr + fmt.Sprintf(", %s: \"%s\"", k, v)
		}
		if fld.Doc == nil {
			utils.CheckErr(errors.New("in fld pointer to Doc is nil"), "type files print in Vue error")
		}
		return fmt.Sprintf("<comp-fld-files v-if=\"this.id != 'new'\" fldName='%s' :label=\"$t('%s')\" :fld='item.%s' :ext = '{tableName: \"%s\", tableId: this.id%s}' @update=\"v=> item.%s = v\" :readonly='%s' %s/>", name, labelI18n, name, fld.Doc.Name, extStr, name, readonly, params)
	case types.FldVueTypeImg:
		extStr := ""
		for k, v := range fld.Vue.Ext {
			extStr = extStr + fmt.Sprintf(", %s: \"%s\"", k, v)
		}
		return fmt.Sprintf("<comp-fld-img v-if=\"this.id != 'new'\" :label=\"$t('%s')\" :fld='item.%s' :ext = '{tableName: \"%s\", tableId: this.id, fldName: \"%s\"%s}' @update=\"v=> item.%s = v\" :readonly='%s' %s/>", labelI18n, name, fld.Doc.Name, name, extStr, name, readonly, params)
	case types.FldVueTypeImgList:
		extStr := ""
		for k, v := range fld.Vue.Ext {
			extStr = extStr + fmt.Sprintf(", %s: \"%s\"", k, v)
		}
		return fmt.Sprintf("<comp-fld-img-list v-if=\"this.id != 'new'\" :label=\"$t('%s')\" :fld='item.%s' :ext = '{tableName: \"%s\", tableId: this.id, fldName: \"%s\"%s}' @update=\"v=> item.%s = v\" :readonly='%s' %s/>", labelI18n, name, fld.Doc.Name, name, extStr, name, readonly, params)
	default:
		return fmt.Sprintf("not found vueFldTemplate for type `%s`", fldType)
	}
}

// печать options для поля select. Находим поле по имени
func PrintFldSelectOptions(doc types.DocType, fldName string) string {
	var fld types.FldType
	// ищем поле по имени
	for _, f := range doc.Flds {
		if f.Name == fldName {
			fld = f
		}
	}
	// проверка что поле нашлось
	if len(fld.Name) == 0 {
		log.Fatalf("PrintFldSelectOptions fld: '%s' not found in doc: '%s'", fldName, doc.Name)
	}
	// проверка что поле имеет тип
	if fld.Vue.Type != types.FldVueTypeSelect {
		log.Fatalf("PrintFldSelectOptions fld: '%s' not type GetFldSelectString in doc: '%s'", fldName, doc.Name)
	}
	res := []string{}
	for _, v := range fld.Vue.Options {
		res = append(res, fmt.Sprintf("{label: '%s', value: '%s'}", v.Label, v.Value))
	}

	return fmt.Sprintf("\t%s", strings.Join(res, ",\n\t\t\t\t\t"))
}


func arrayStringJoin(arr []string) string  {
	tmpArr := []string{}
	for _, v := range arr {
		tmpArr = append(tmpArr, fmt.Sprintf(`"%s"`, v))
	}
	return strings.Join(tmpArr, ", ")
}

func getCurrentDir() string  {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatalf("ParseTemplates runtime.Caller: No caller information")
	}
	return path.Dir(filename)
}