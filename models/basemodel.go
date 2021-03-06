package models

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/stoewer/go-strcase"
	"github.com/t2wu/betterrest/libs/datatypes"
	"github.com/t2wu/betterrest/libs/urlparam"
	"github.com/t2wu/betterrest/libs/utils/jsontrans"

	"github.com/asaskevich/govalidator"
	"github.com/jinzhu/gorm"
)

// UserRole type with enum
type UserRole int

const (
	// Negatives are not stored in DB

	// UserRoleAny not for value in db, but for permission where any is fine (link table)
	UserRoleAny UserRole = -2

	// UserRoleInvalid is invalid for this resource
	UserRoleInvalid UserRole = -1

	// UserRoleAdmin is admin UserRole
	UserRoleAdmin UserRole = 0

	// UserRoleGuest is guest UserRole (screw go-lint man)
	UserRoleGuest UserRole = 1

	// UserRolePublic to all (global object)
	UserRolePublic UserRole = 2
)

type UserStatus int

const (
	UserStatusUnverified UserStatus = iota // email unverified (can happen with a change of email)
	UserStatusActive                       // email active
	UserStatusInactive                     // email inactive (for whatever reason the account is inactivated)
)

// BaseModel is the base class domain model which has standard ID
type BaseModel struct {
	// For MySQL
	// ID        *datatypes.UUID `gorm:"type:binary(16);primary_key;" json:"id"`

	// For Postgres
	ID        *datatypes.UUID `gorm:"type:uuid;primary_key;" json:"id"`
	CreatedAt time.Time       `sql:"index" json:"createdAt" json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	DeletedAt *time.Time      `sql:"index" json:"deletedAt"`

	// Ownership with the most previledged permission can delete the device and see every field.
	// So there can be an ownership number, say 3, and that maps to a permission type
	// (within the ownership table), say "admin ownership" (int 0), and whoever is a member of ownership
	// 3 thus has the admin priviledge
	// The "guest" of model "device" and "guest" of model of "scene" is vastly different, because
	// the fields are different, and specific field permission is based on priviledge -> field mapping
	// defined when getting permission()
	// Ownership []int64
}

// GetID Get the ID field of the model (useful when using interface)
func (b *BaseModel) GetID() *datatypes.UUID {
	return b.ID
}

// SetID Set the ID field of the model (useful when using interface)
func (b *BaseModel) SetID(id *datatypes.UUID) {
	b.ID = id
}

// BeforeCreate sets a UUID if no ID is set
// (this is Gorm's hookpoint)
func (b *BaseModel) BeforeCreate(scope *gorm.Scope) error {
	if b.ID == nil {
		uuid := datatypes.NewUUID()
		return scope.SetColumn("ID", uuid)
	}

	return nil
}

// Validate validates the model
func (b *BaseModel) Validate() error {
	if ok, err := govalidator.ValidateStruct(b); ok == false && err != nil {
		return err
	}
	return nil
}

// ModelHasOwnership is the standard domain model to embed when creating
// an ownership type. If you need a customized linking table,
// Embed a BaseModel instead, and define a gorm "PRELOAD:false", json "-",
// and betterrest:"ownership"
type ModelHasOwnership struct {
	BaseModel

	Ownerships []OwnershipModelWithIDBase `gorm:"PRELOAD:false" json:"-" betterrest:"ownership"`
}

// IModel is the interface for all domain models
type IModel interface {
	// Permissions(role UserRole, scope *string) jsontrans.JSONFields

	// The following two avoids having to use reflection to access ID
	GetID() *datatypes.UUID
	SetID(id *datatypes.UUID)
}

// IHasPermissions is for IModel with a custom permission field to cherry pick json fields
// default is to return all but the dates
type IHasPermissions interface {
	Permissions(role UserRole, who Who) (jsontrans.Permission, jsontrans.JSONFields)
}

// IHasRenderer is for formatting IModel with a custom function
// basically do your own custom output
// For batch renderer, register a Render(r UserRole, who Who, modelObjs []IModel) []byte
type IHasRenderer interface {
	Render(role UserRole, who Who) []byte
}

// This is registered
// // IHasBatchRender is for formatting []IModel with a custom function
// // basically do your own custom output
// type IHasBatchRender interface {
// 	Render(r UserRole, who Who, modelObjs []IModel) []byte
// }

// Inside content is an array of JSONIDPatch
// {
// content:[
// {
//   "id": "2f9795fd-fb39-4ea5-af69-14bfa69840aa",
//   "patches": [
// 	  { "op": "test", "path": "/a/b/c", "value": "foo" },
// 	  { "op": "remove", "path": "/a/b/c" },
//   ]
// }
// ]
// }

// JSONIDPatch is the stuff inside "content" for PatchMany operation
type JSONIDPatch struct {
	ID    *datatypes.UUID `json:"id"`
	Patch json.RawMessage `json:"patch"` // json.RawMessage is actually just typedefed to []byte
}

// -------------------------

// OrgModelTypeFromOrgResourceTypeString given org resource typeString
// returns the reflect type of the organization
func OrgModelTypeFromOrgResourceTypeString(typeString string) reflect.Type {
	// if ModelRegistry[typeString].Mapper != MapperTypeViaOrganization {
	// 	// Programming error
	// 	panic(fmt.Sprintf("TypeString %s does not represents a resource under organization", typeString))
	// }

	// return reflect.TypeOf(NewOrgModelFromOrgResourceTypeString(typeString))

	if ModelRegistry[typeString].Mapper != MapperTypeViaOrganization {
		// Programming error
		panic(fmt.Sprintf("TypeString %s does not represents a resource under organization", typeString))
	}

	orgTypeString := ModelRegistry[typeString].OrgTypeString
	return ModelRegistry[orgTypeString].Typ
}

// ----------------------------
// The new models for all the link tables

// NewOrgModelFromOrgResourceTypeString gets Organization object
// If you're a resource under hooked up by Organization
func NewOrgModelFromOrgResourceTypeString(typeString string) IModel {
	if ModelRegistry[typeString].Mapper != MapperTypeViaOrganization {
		// Programming error
		panic(fmt.Sprintf("TypeString %s does not represents a resource under organization", typeString))
	}

	orgTypeString := ModelRegistry[typeString].OrgTypeString
	return reflect.New(ModelRegistry[orgTypeString].Typ).Interface().(IModel)
}

// NewOrgOwnershipModelFromOrgResourceTypeString gets the joining table from the resource's
// organization model to the user
func NewOrgOwnershipModelFromOrgResourceTypeString(typeString string) IModel {
	if ModelRegistry[typeString].Mapper != MapperTypeViaOrganization {
		// Programming error
		panic(fmt.Sprintf("TypeString %s does not represents a resource under organization", typeString))
	}

	orgTypeString := ModelRegistry[typeString].OrgTypeString // org is an ownership resource
	return NewOwnershipModelFromOwnershipResourceTypeString(orgTypeString)
}

// NewOwnershipModelFromOwnershipResourceTypeString returns the model object
// of the ownership table (the table that links from this resource represented by the type string
// to the user)
func NewOwnershipModelFromOwnershipResourceTypeString(typeString string) IModel {
	if ModelRegistry[typeString].Mapper != MapperTypeViaOwnership {
		// Programming error
		panic(fmt.Sprintf("TypeString %s does not represents a resource under organization", typeString))
	}

	// Either custom one or the default one
	typ := ModelRegistry[typeString].OwnershipType

	return reflect.New(typ).Interface().(IModel)
}

// ----------------------------
// The new linking table names

// OrgModelNameFromOrgResourceTypeString given org resource typeString,
// returns organization table name
func OrgModelNameFromOrgResourceTypeString(typeString string) string {
	m := NewOrgModelFromOrgResourceTypeString(typeString)
	return GetTableNameFromIModel(m)
}

// OrgOwnershipModelNameFromOrgResourceTypeString given org resource typeString,
// returns name of organization table's linking table (ownership table) to user
func OrgOwnershipModelNameFromOrgResourceTypeString(typeString string) string {
	m := NewOrgOwnershipModelFromOrgResourceTypeString(typeString)
	return GetTableNameFromIModel(m)
}

// OwnershipTableNameFromOwnershipResourceTypeString given ownership resource typeStirng
// returns name of ownership table to the user
func OwnershipTableNameFromOwnershipResourceTypeString(typeString string) string {
	// m := NewOwnershipModelFromOwnershipResourceTypeString(typeString)

	// Either custom one or the default one

	tableName := *ModelRegistry[typeString].OwnershipTableName

	if tableName == "ownership_model_with_id_base" {
		m := reflect.New(ModelRegistry[typeString].Typ).Interface().(IModel)
		tableName = "user_owns_" + GetTableNameFromIModel(m)
	}

	return tableName
}

// ----------------------------

// IDoRealDelete is an interface to customize specification for real db delete
type IDoRealDelete interface {
	DoRealDelete() bool
}

// HTTP stores HTTP request information
type HTTP struct {
	Endpoint string
	Method   string
}

// IGuardAPIEntry supports method which guard access to API based on scope
type IGuardAPIEntry interface {
	GuardAPIEntry(models Who, http HTTP) bool
}

// ModelCargo is payload between hookpoints
type ModelCargo struct {
	Payload interface{}
}

// CRUPDOp designates the type of operations for BeforeCRUPD and AfterCRUPD hookpoints
type CRUPDOp int

const (
	CRUPDOpRead CRUPDOp = iota
	CRUPDOpCreate
	CRUPDOpUpdate
	CRUPDOpPatch
	CRUPDOpDelete
)

// HookPointData is the data send to single model hookpoints
type HookPointData struct {
	// DB handle
	DB *gorm.DB
	// Who is the user information, who is operating this CRUPD right now
	Who Who
	// TypeString is the typeString (model string) of this model
	TypeString string
	// Cargo between Before and After hookpoints (not used in IAfterRead since there is no IBeforeRead.)
	Cargo *ModelCargo
	// Role of this user in relation to this data, only available during read
	Role *UserRole
	// URL parameters
	URLParams *map[urlparam.Param]interface{}
}

// IBeforeLogin has a function that is a hookpoint for actions before login but after marshalling
type IBeforeLogin interface {
	BeforeLogin(hpdata HookPointData) error
}

// IAfterLogin has a function that is a hookpoint for actions after login
type IAfterLogin interface {
	AfterLogin(hpdata HookPointData, payload map[string]interface{}) (map[string]interface{}, error)
}

// IAfterLoginFailed has a function that is a hookpoint for actions after login but before marshalling
type IAfterLoginFailed interface {
	AfterLoginFailed(hpdata HookPointData) error
}

// IBeforePasswordChange has a function that is a hookpoint for actions before password change but before marshalling
type IBeforePasswordChange interface {
	BeforePasswordChange(hpdata HookPointData) error
}

// IAfterPasswordChange has a function that is a hookpoint for actions after password change
type IAfterPasswordChange interface {
	AfterPasswordChange(hpdata HookPointData) error
}

// IBeforeCreate supports method to be called before data is inserted (created) into the database
type IBeforeCreate interface {
	BeforeCreateDB(hpdata HookPointData) error
}

// IBeforeUpdate supports method to be called before data is updated in the database
type IBeforeUpdate interface {
	BeforeUpdateDB(hpdata HookPointData) error
}

// IBeforePatchApply supports method to be called before data is patched in the database
// And also before the patch is applied. This comes before BeforePatchDB
type IBeforePatchApply interface {
	BeforePatchApplyDB(hpdata HookPointData) error
}

// IBeforePatch supports method to be called before data is patched in the database
type IBeforePatch interface {
	BeforePatchDB(hpdata HookPointData) error
}

// IBeforeDelete supports method to be called before data is deleted from the database
type IBeforeDelete interface {
	BeforeDeleteDB(hpdata HookPointData) error
}

// IAfterRead supports method to be called after data is read from the database
type IAfterRead interface {
	AfterReadDB(hpdata HookPointData) error
}

// IAfterCreate supports method to be called after data is inserted (created) into the database
type IAfterCreate interface {
	AfterCreateDB(hpdata HookPointData) error
}

// IAfterUpdate supports method to be called after data is updated in the database
type IAfterUpdate interface {
	AfterUpdateDB(hpdata HookPointData) error
}

// IAfterPatch supports method to be called before data is patched in the database
type IAfterPatch interface {
	AfterPatchDB(hpdata HookPointData) error
}

// IAfterDelete supports method to be called after data is deleted from the database
type IAfterDelete interface {
	AfterDeleteDB(hpdata HookPointData) error
}

// IBeforeCUPD supprots method to be called before data is after all CRUPD operations
// This is called before the individual ops
type IBeforeCUPD interface {
	BeforeCUPDDB(hpdata HookPointData, op CRUPDOp) error
}

// IAfterCRUPD supprots method to be called after data is after all CRUPD operations
// This is called before the individual ops
type IAfterCRUPD interface {
	AfterCRUPDDB(hpdata HookPointData, op CRUPDOp) error
}

// IValidate supports validation with govalidator
type IValidate interface {
	Validate(who Who, http HTTP) error
}

// ------------------------------------

// IOwnership is what OwnershipModelBase tables should satisfy.
// Except OwnershipType, that's for struct which embed OwnershipModelBase
type IOwnership interface {
	GetRole() UserRole
	SetRole(UserRole)

	GetUserID() *datatypes.UUID
	SetUserID(*datatypes.UUID)

	GetModelID() *datatypes.UUID
	SetModelID(*datatypes.UUID)

	GetID() *datatypes.UUID
	SetID(*datatypes.UUID)

	// OwnershipType() IOwnership
}

// OwnershipModelBase has a role. Intended to be embedded
// by table serving as link from resource to user
type OwnershipModelBase struct {
	ID *datatypes.UUID `gorm:"type:uuid;primary_key;" json:"id"`

	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `sql:"index" json:"deletedAt"`

	Role UserRole `json:"role"` // an int
}

// BeforeCreate sets a UUID if no ID is set
// (this is Gorm's hookpoint)
func (o *OwnershipModelBase) BeforeCreate(scope *gorm.Scope) error {
	if o.ID == nil {
		uuid := datatypes.NewUUID()
		return scope.SetColumn("ID", uuid)
	}

	return nil
}

// GetRole gets the role field of the model, comforms to IOwnership
func (o *OwnershipModelBase) GetRole() UserRole {
	return o.Role
}

// SetRole sets the role field of the model, comforms to IOwnership
func (o *OwnershipModelBase) SetRole(r UserRole) {
	o.Role = r
}

// GetID Get the ID field of the model (useful when using interface)
func (o *OwnershipModelBase) GetID() *datatypes.UUID {
	return o.ID
}

// SetID Set the ID field of the model (useful when using interface)
func (o *OwnershipModelBase) SetID(id *datatypes.UUID) {
	o.ID = id
}

// OwnershipModelWithIDBase is one with ID, if you don't need unique index
// for userID and modelID (if you don't expose the link table via LinkTableMapper)
// You can use this.
type OwnershipModelWithIDBase struct {
	OwnershipModelBase

	UserID  *datatypes.UUID `gorm:"index" json:"userID"` // I guess the user's table has to be named "User" then.
	ModelID *datatypes.UUID `gorm:"index" json:"modelID"`
}

// To comform to IModel, embedding functions don't work

// GetID Get the ID field of the model (useful when using interface)
func (o *OwnershipModelWithIDBase) GetID() *datatypes.UUID {
	return o.ID
}

// SetID Set the ID field of the model (useful when using interface)
func (o *OwnershipModelWithIDBase) SetID(id *datatypes.UUID) {
	o.ID = id
}

// GetUserID gets the user id of the model, comforms to IOwnership
func (o *OwnershipModelWithIDBase) GetUserID() *datatypes.UUID {
	return o.UserID
	// v := reflect.ValueOf(o)
	// return reflect.Indirect(v).FieldByName("ID").Interface().(*datatypes.UUID)
}

// SetUserID sets the user id of the model, comforms to IOwnership
func (o *OwnershipModelWithIDBase) SetUserID(id *datatypes.UUID) {
	o.UserID = id
}

// SetModelID sets the id of the model, comforms to IOwnership
func (o *OwnershipModelWithIDBase) SetModelID(id *datatypes.UUID) {
	o.ModelID = id
}

// GetModelID gets the id of the model, comforms to IOwnership
func (o *OwnershipModelWithIDBase) GetModelID() *datatypes.UUID {
	return o.ModelID
}

// ---------------

// IHasTableName we know if there is Gorm's defined custom TableName
type IHasTableName interface {
	TableName() string
}

// GetTableNameFromIModel gets table name from an IModel
func GetTableNameFromIModel(model IModel) string {
	var tableName string
	if m, ok := model.(IHasTableName); ok {
		tableName = m.TableName()
	} else {
		tableName = reflect.TypeOf(model).String()
		// If it is something like "models.XXX", we only want the stuff ater "."
		if strings.Contains(tableName, ".") {
			tableName = strings.Split(tableName, ".")[1]
		}

		tableName = strcase.SnakeCase(tableName)
	}

	// If it's a pointer, get rid of "*"
	if strings.HasPrefix(tableName, "*") {
		tableName = tableName[1:]
	}

	return tableName
}

// GetTableNameFromTypeString get table name from typeString
func GetTableNameFromTypeString(typeString string) string {
	model := NewFromTypeString(typeString)
	return GetTableNameFromIModel(model)
}

// GetTableNameFromType get table name from the model reflect.type
func GetTableNameFromType(typ reflect.Type) string {
	model := reflect.New(typ).Interface().(IModel)
	return GetTableNameFromIModel(model)
}

// ----------------

func GetTagValueFromModelByTagKeyBetterRestAndValueKey(modelObj interface{}, valueKey string) *string {
	v := reflect.Indirect(reflect.ValueOf(modelObj))
	for i := 0; i < v.NumField(); i++ {
		// t := v.Type().Field(i).Tag.Get(tag) // if no tag, empty string
		if tagVal, ok := v.Type().Field(i).Tag.Lookup("betterrest"); ok {
			pairs := strings.Split(tagVal, ";")
			for _, pair := range pairs {
				if strings.HasPrefix(pair, valueKey) {
					return &pair
				}
			}
		}
	}
	return nil
}

// GetFieldNameFromModelByTagKey get's the name of the tagged field
// If it's a slice, it returns the element type
// It's an interface{} because it's not necessarily IModel
func GetFieldNameFromModelByTagKey(modelObj interface{}, valueKey string) *string {
	v := reflect.Indirect(reflect.ValueOf(modelObj))
	for i := 0; i < v.NumField(); i++ {
		if tagVal, ok := v.Type().Field(i).Tag.Lookup("betterrest"); ok {
			pairs := strings.Split(tagVal, ";")
			for _, pair := range pairs {
				if strings.HasPrefix(pair, valueKey) {
					s := v.Type().Field(i).Name
					return &s
				}
			}

		}
	}
	return nil
}

// GetFieldValueFromModelByTagKeyBetterRestAndValueKey fetches value of the variable tagged in tag
func GetFieldValueFromModelByTagKeyBetterRestAndValueKey(modelObj IModel, valueKey string) interface{} {
	v := reflect.Indirect(reflect.ValueOf(modelObj))
	for i := 0; i < v.NumField(); i++ {
		if tagVal, ok := v.Type().Field(i).Tag.Lookup("betterrest"); ok {
			pairs := strings.Split(tagVal, ";")
			for _, pair := range pairs {
				if strings.HasPrefix(pair, valueKey) {
					return v.Field(i).Interface()
				}
			}
		}
	}
	return nil
}

// GetFieldTypeFromModelByTagKeyBetterRestAndValueKey fetches the datatype of the variable tagged in tag
func GetFieldTypeFromModelByTagKeyBetterRestAndValueKey(modelObj IModel, valueKey string, recurseIntoEmbedded bool) reflect.Type {
	v := reflect.Indirect(reflect.ValueOf(modelObj))
	return getFieldTypeFromModelByTagKeyBetterRestAndValueKeyCore(v, valueKey, recurseIntoEmbedded)
}

func getFieldTypeFromModelByTagKeyBetterRestAndValueKeyCore(v reflect.Value, valueKey string, recurseIntoEmbedded bool) reflect.Type {
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Anonymous && recurseIntoEmbedded {
			embeddedModel := v.Field(i)
			ret := getFieldTypeFromModelByTagKeyBetterRestAndValueKeyCore(embeddedModel, valueKey, recurseIntoEmbedded)
			if ret != nil {
				return ret
			} // else continues
		} else if tagVal, ok := v.Type().Field(i).Tag.Lookup("betterrest"); ok {
			pairs := strings.Split(tagVal, ";")
			for _, pair := range pairs {
				if strings.HasPrefix(pair, valueKey) {
					fieldVal := v.Field(i)
					switch fieldVal.Kind() {
					case reflect.Slice:
						return v.Type().Field(i).Type.Elem() // This work even when slice is empty
					default:
						// return fieldVal.Type()
						return v.Type().Field(i).Type
					}
				}
			}
		}
	}
	return nil
}
