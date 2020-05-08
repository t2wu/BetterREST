package datamapper

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"sync"

	"github.com/t2wu/betterrest/libs/datatypes"
	"github.com/t2wu/betterrest/libs/security"
	"github.com/t2wu/betterrest/models"

	"github.com/jinzhu/gorm"
)

var onceUser sync.Once
var usercrud *UserMapper

// UserMapper is a User CRUD manager
type UserMapper struct {
}

// SharedUserMapper creats a singleton of Crud object
func SharedUserMapper() *UserMapper {
	onceUser.Do(func() {
		usercrud = &UserMapper{}
	})

	return usercrud
}

//------------------------
// User specific CRUD
// Cuz user is spcial, need to create ownership and no need to check for owner
// ------------------------------------

// CreateOne creates an user model based on json and store it in db
// Also creates a ownership with admin access
func (mapper *UserMapper) CreateOne(db *gorm.DB, oid *datatypes.UUID, typeString string, modelObj models.IModel) (models.IModel, error) {
	// Special case, there is really no oid in this case, user doesn't exist yet

	// modelObj is a a User struct, but we cannot do type assertion because library user
	// should define it. If we make an interface with user.Ownership setter and getter,
	// we need to ask library user to define a user.Ownership setter and getter, it's too
	// much hassle
	password := reflect.ValueOf(modelObj).Elem().FieldByName(("Password")).Interface().(string)

	// Additional checking because password should not be blank with create
	if password == "" {
		log.Println("password should not be blank!!!")
		return nil, fmt.Errorf("password should not be blank")
	}

	ownership := reflect.ValueOf(modelObj).Elem().FieldByName("Ownerships")
	ownership.Set(reflect.MakeSlice(reflect.SliceOf(models.OwnershipTyp), 1, 1))
	ownership.Index(0).Set(reflect.New(models.OwnershipTyp).Elem())

	hash, err := security.HashAndSalt(password)
	if err != nil {
		return nil, err
	}

	reflect.ValueOf(modelObj).Elem().FieldByName("PasswordHash").Set(reflect.ValueOf(hash))

	// there isn't really an oid at this point
	return CreateWithHooks(db, oid, "users", modelObj)
}

// GetOneWithID get one model object based on its type and its id string
func (mapper *UserMapper) GetOneWithID(db *gorm.DB, oid *datatypes.UUID, typeString string, id datatypes.UUID) (models.IModel, models.UserRole, error) {
	// TODO: Currently can only read ID from your own (not others in the admin group either)
	db = db.Set("gorm:auto_preload", true)

	// Todo: maybe guest shoud be able to read some fields
	if id.String() != oid.String() {
		return nil, 0, errPermission
	}

	modelObj := models.NewFromTypeString(typeString)
	modelObj.SetID(oid)

	if err := db.First(modelObj).Error; err != nil {
		return nil, 0, err
	}

	return modelObj, models.Admin, nil
}

// UpdateOneWithID updates model based on this json
func (mapper *UserMapper) UpdateOneWithID(db *gorm.DB, oid *datatypes.UUID, typeString string, modelObj models.IModel, id datatypes.UUID) (models.IModel, error) {
	log.Println("userMapper's UpdateOneWithID called")
	if err := checkErrorBeforeUpdate(mapper, db, oid, typeString, modelObj, id); err != nil {
		return nil, err
	}

	if *oid != id {
		return nil, errPermission
	}

	password := reflect.ValueOf(modelObj).Elem().FieldByName(("Password")).Interface().(string)

	// Additional checking because password should not be blank with update
	if password == "" {
		log.Println("password should not be blank!!!")
		return nil, fmt.Errorf("password should not be blank")
	}

	hash, err := security.HashAndSalt(password)
	if err != nil {
		return nil, err
	}
	reflect.ValueOf(modelObj).Elem().FieldByName("PasswordHash").Set(reflect.ValueOf(hash))

	cargo := models.ModelCargo{}

	// Before hook
	if v, ok := modelObj.(models.IBeforeUpdate); ok {
		if err := v.BeforeUpdateDB(db, oid, typeString, &cargo); err != nil {
			return nil, err
		}
	}

	modelObj2, err := updateOneCore(mapper, db, oid, typeString, modelObj, id)
	if err != nil {
		return nil, err
	}

	// After hook
	if v, ok := modelObj2.(models.IAfterUpdate); ok {
		if err = v.AfterUpdateDB(db, oid, typeString, &cargo); err != nil {
			return nil, err
		}
	}

	return modelObj2, nil
}

// DeleteOneWithID deletes the user with the ID
func (mapper *UserMapper) DeleteOneWithID(db *gorm.DB, oid *datatypes.UUID, typeString string, id datatypes.UUID) (models.IModel, error) {
	if id.UUID.String() == "" {
		return nil, errIDEmpty
	}

	// Pull out entire modelObj
	modelObj, role, err := mapper.GetOneWithID(db, oid, typeString, id)
	if err != nil { // Error is "record not found" when not found
		return nil, err
	}
	if role != models.Admin {
		// even if found, not authorized, so return a not found
		// but how do I do that here?
		return nil, errors.New("not found")
	}

	cargo := models.ModelCargo{}

	// Before delete hookpoint
	if v, ok := modelObj.(models.IBeforeDelete); ok {
		err = v.BeforeDeleteDB(db, oid, typeString, &cargo)
		if err != nil {
			return nil, err
		}
	}

	// Unscoped() for REAL delete!
	// Otherwise my constraint won't work...
	// Soft delete will take more work, have to verify myself manually
	// db.Unscoped().Delete(modelObj).Error
	err = db.Delete(modelObj).Error
	if err != nil {
		return nil, err
	}

	// After delete hookpoint
	if v, ok := modelObj.(models.IAfterDelete); ok {
		err = v.AfterDeleteDB(db, oid, typeString, &cargo)
		if err != nil {
			return nil, err
		}
	}

	return modelObj, nil
}