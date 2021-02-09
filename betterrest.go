package betterrest

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/jinzhu/gorm"
	"github.com/t2wu/betterrest/models"
)

/*
 * Registration
 */

// RegUserModel register the user model
func RegUserModel(typeString string, modelObj models.IModel) {
	options := models.RegOptions{BatchMethods: "CRUPD", IdvMethods: "RUPD", Mapper: models.MapperTypeUser}
	RegModelWithOption(typeString, modelObj, options)
	models.UserTyp = reflect.TypeOf(modelObj).Elem()
}

// RegModel adds a New function for an models.IModel
func RegModel(typeString string, modelObj models.IModel) {
	options := models.RegOptions{BatchMethods: "CRUPD", IdvMethods: "RUPD", Mapper: models.MapperTypeViaOwnership}
	RegModelWithOption(typeString, modelObj, options)
}

// RegModelWithOption adds a New function for an models.IModel
func RegModelWithOption(typeString string, modelObj models.IModel, options models.RegOptions) {
	if _, ok := models.ModelRegistry[typeString]; ok {
		panic(fmt.Sprintf("%s should not register the same type string twice:", typeString))
	}

	models.ModelRegistry[typeString] = &models.Reg{}

	reg := models.ModelRegistry[typeString] // pointer type
	reg.Typ = reflect.TypeOf(modelObj).Elem()
	reg.CreateObj = modelObj

	if options.BatchMethods == "" {
		reg.BatchMethods = "CRUPD"
	} else {
		reg.BatchMethods = options.BatchMethods
	}

	if options.IdvMethods == "" {
		reg.IdvMethods = "RUPD"
	} else {
		reg.IdvMethods = options.IdvMethods
	}

	// Default 0 is ownershipmapper
	reg.Mapper = options.Mapper

	switch options.Mapper {
	case models.MapperTypeViaOwnership:
		typ := models.GetFieldTypeFromModelByTagKeyBetterRestAndValueKey(modelObj, "ownership")
		if typ == nil {
			panic(fmt.Sprintf("%s missing betterrest:\"ownership\" tag", typeString))
		}
		m := reflect.New(typ).Interface().(models.IModel)
		s := models.GetTableNameFromIModel(m)
		reg.OwnershipTableName = &s
		reg.OwnershipType = typ
	case models.MapperTypeViaOrganization:
		// We want the model type. So we get that by getting name first
		// since the foreign key field name is always nameID
		v := models.GetValueFromModelByTagKeyBetterRestAndValueKey(modelObj, "org")
		if v == nil {
			panic(fmt.Sprintf("%s missing betterrest:\"org:typeString\" tag", typeString))
		}
		val := *v
		if !strings.Contains(val, "org:") {
			panic(fmt.Sprintf("%s missing tag value for betterrest:\"org:typeString\"", typeString))
		}

		toks := strings.Split(val, "org:")
		reg.OrgTypeString = toks[1]
	}

	// Check if there is any struct or element of IModel which has no betterrest:"peg" or "peg-associate"
	// field. There should be a designation for every struct unless it's ownership or org table
	// Traverse through the tree

	checked := make(map[string]bool)
	checkAllFieldsForBetterTags(modelObj, checked)

	// panic(fmt.Sprintf("all checked %+v", checked))

}

// checked is needed because it can be recursive in a pegassoc-manytomany
func checkAllFieldsForBetterTags(modelObj models.IModel, checked map[string]bool) {
	modelName := reflect.TypeOf(modelObj).Elem().Name()
	if _, ok := checked[modelName]; ok { // if already checked
		return
	}
	checked[modelName] = true

	v := reflect.Indirect(reflect.ValueOf(modelObj))
	for i := 0; i < v.NumField(); i++ {
		fieldName := v.Type().Field(i).Name
		if fieldName != "BaseModel" && fieldName != "OwnershipModelBase" && fieldName != "OwnershipModelWithIDBase" {
			var nextType reflect.Type
			switch v.Field(i).Kind() {
			case reflect.Ptr: // if it's datatypes.UUID you don't dig further
				nextType = v.Type().Field(i).Type.Elem()
				// if UUID, no need to traverse more
				if nextType.String() == "datatypes.UUID" {
					continue
				}
				// Then only check if it's a struct
				if nextType.Kind() == reflect.Struct {
					tagVal := v.Type().Field(i).Tag.Get("betterrest")
					checkTagValue(tagVal, fieldName, modelName)
				}
				// log.Println("fieldName:", fieldName)
				// nextType = v.Type().Field(i).Type.Elem()
			case reflect.Struct:
				tagVal := v.Type().Field(i).Tag.Get("betterrest")
				checkTagValue(tagVal, fieldName, modelName)

				nextType = v.Type().Field(i).Type
			case reflect.Slice:
				tagVal := v.Type().Field(i).Tag.Get("betterrest")
				checkTagValue(tagVal, fieldName, modelName)

				nextType = v.Type().Field(i).Type.Elem()
			}

			// TrackerClassPredicateAction
			if nextType != nil {
				// only array []*model will work, what now? if it's not array?
				if nextModel, ok := reflect.New(nextType).Interface().(models.IModel); ok {
					checkAllFieldsForBetterTags(nextModel, checked) // how to get the name of struct
				}
			}
		}
	}
	// return nil
}

func checkTagValue(tagVal, fieldName, modelName string) {
	pairs := strings.Split(tagVal, ";")
	for _, pair := range pairs {
		if pair != "peg" && !strings.HasPrefix(pair, "pegassoc") &&
			!strings.HasPrefix(pair, "ownership") && !strings.HasPrefix(pair, "org") &&
			!strings.HasPrefix(pair, "peg-ignore") && !strings.HasPrefix(pair, "pegassoc-manytomany") {
			panic(fmt.Sprintf("%s in %s struct or array with the exception of UUID should have one of the following tag: peg, pegassoc, pegassoc-manytomany, ownership, org, or peg-ignore", fieldName, modelName))
		}
	}
}

// RegCustomCreate register custom create table funtion
func RegCustomCreate(typeString string, modelObj models.IModel, f func(db *gorm.DB) (*gorm.DB, error)) {
	reg := models.ModelRegistry[typeString] // pointer type
	reg.CreateObj = modelObj
	reg.CreateMethod = f
}

// RegBatchCRUPDHooks adds hookpoints which are called before
// CUPD (no read) and after batch CRUPD. Either one can be left as nil
func RegBatchCRUPDHooks(typeString string,
	before func(bhpData models.BatchHookPointData, crupdOp models.CRUPDOp) error,
	after func(bhpData models.BatchHookPointData, crupdOp models.CRUPDOp) error) {

	if _, ok := models.ModelRegistry[typeString]; !ok {
		models.ModelRegistry[typeString] = &models.Reg{}
	}

	models.ModelRegistry[typeString].BeforeCUPD = before
	models.ModelRegistry[typeString].AfterCRUPD = after
}

// RegBatchInsertHooks adds hookpoints which are called before
// and after batch update. Either one can be left as nil
func RegBatchInsertHooks(typeString string,
	before func(bhpData models.BatchHookPointData) error,
	after func(bhpData models.BatchHookPointData) error) {

	if _, ok := models.ModelRegistry[typeString]; !ok {
		models.ModelRegistry[typeString] = &models.Reg{}
	}

	models.ModelRegistry[typeString].BeforeInsert = before
	models.ModelRegistry[typeString].AfterInsert = after
}

// RegBatchReadHooks adds hookpoints which are called after
// and read, can be left as nil
func RegBatchReadHooks(typeString string,
	after func(bhpData models.BatchHookPointData) error) {

	if _, ok := models.ModelRegistry[typeString]; !ok {
		models.ModelRegistry[typeString] = &models.Reg{}
	}

	models.ModelRegistry[typeString].AfterRead = after
}

// RegBatchUpdateHooks adds hookpoints which are called before
// and after batch update. Either one can be left as nil
func RegBatchUpdateHooks(typeString string,
	before func(bhpData models.BatchHookPointData) error,
	after func(bhpData models.BatchHookPointData) error) {

	if _, ok := models.ModelRegistry[typeString]; !ok {
		models.ModelRegistry[typeString] = &models.Reg{}
	}

	models.ModelRegistry[typeString].BeforeUpdate = before
	models.ModelRegistry[typeString].AfterUpdate = after
}

// RegBatchPatchHooks adds hookpoints which are called before
// and after batch update. Either one can be left as nil
func RegBatchPatchHooks(typeString string,
	before func(bhpData models.BatchHookPointData) error,
	after func(bhpData models.BatchHookPointData) error) {

	if _, ok := models.ModelRegistry[typeString]; !ok {
		models.ModelRegistry[typeString] = &models.Reg{}
	}

	models.ModelRegistry[typeString].BeforePatch = before
	models.ModelRegistry[typeString].AfterPatch = after
}

// RegBatchDeleteHooks adds hookpoints which are called before
// and after batch delete. Either one can be left as nil
func RegBatchDeleteHooks(typeString string,
	before func(bhpData models.BatchHookPointData) error,
	after func(bhpData models.BatchHookPointData) error) {

	if _, ok := models.ModelRegistry[typeString]; !ok {
		models.ModelRegistry[typeString] = &models.Reg{}
	}

	models.ModelRegistry[typeString].BeforeDelete = before
	models.ModelRegistry[typeString].AfterDelete = after
}

// AutoMigrate all dbs
// Commented out because I haven't figure out about how to handle
// primary key dependencies yet. Or does Gorm do it in a newer version
// Order matters. A table has to exist first.
// func AutoMigrate() {
// 	for typeString, reg := range models.ModelRegistry {
// 		log.Println("=============creating db:", typeString)
// 		d := db.Shared()

// 		// CreateObject is defined when register the model
// 		// But it could be overridden by RegCustomCreate
// 		// and can be overrridden to be nil
// 		if reg.CreateObj != nil {
// 			d.AutoMigrate(reg.CreateObj)
// 		}

// 		if reg.Mapper == models.MapperTypeViaOwnership {
// 			log.Println("=============creating default ownership table:", typeString)
// 			// Search for custom ownership, otherwise the automatic one
// 			// reflect.New(OwnershipType)
// 			d.Table(*reg.OwnershipTableName).AutoMigrate(reflect.New(reg.OwnershipType))
// 		}

// 		// In addition, RegCustomCreate cna define a CreateMethod
// 		// which handles any additional create procedure or the actual create procedure
// 		// This is run in addition to CreateObj unless CreateObj is set null
// 		if reg.CreateMethod != nil {
// 			log.Println("has custom create")
// 			reg.CreateMethod(d)
// 		}
// 	}
// }