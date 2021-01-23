package datamapper

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/stoewer/go-strcase"
	"github.com/t2wu/betterrest/libs/datatypes"
	"github.com/t2wu/betterrest/models"

	"github.com/jinzhu/gorm"
)

// ---------------------------------------

// createOneCoreOrganization creates a user
func createOneCoreOrganization(db *gorm.DB, oid *datatypes.UUID, typeString string, modelObj models.IModel) (models.IModel, error) {
	// No need to check if primary key is blank.
	// If it is it'll be created by Gorm's BeforeCreate hook
	// (defined in base model)
	// if dbc := db.Create(modelObj); dbc.Error != nil {
	if dbc := db.Create(modelObj); dbc.Error != nil {
		// create failed: UNIQUE constraint failed: user.email
		// It looks like this error may be dependent on the type of database we use
		return nil, dbc.Error
	}

	// For pegassociated, the since we expect association_autoupdate:false
	// need to manually create it
	if err := createPeggedAssocFields(db, modelObj); err != nil {
		return nil, err
	}

	// For table with trigger which update before insert, we need to load it again
	if err := db.First(modelObj).Error; err != nil {
		// That's weird. we just inserted it.
		return nil, err
	}

	return modelObj, nil
}

func userHasRolesAccessToModelOrg(db *gorm.DB, oid *datatypes.UUID, typeString string, modelObj models.IModel, roles []models.UserRole) (bool, error) {
	var modelObjHavingOrganization models.IHasOrganizationLink
	var modelObjHavingOwnership models.IHasOwnershipLink
	var ok bool

	// Create one model (dock),
	// Make sure oid has admin access to the organization it refers to

	// (Maybe organization should be defined in the library)
	// And it's organizational type has a user which includes

	if modelObjHavingOrganization, ok = modelObj.(models.IHasOrganizationLink); !ok {
		return false, fmt.Errorf("Model %s does not comform to IHasOrganizationLink", typeString)
	}

	organizationTableName := models.GetOrganizationTableName(modelObjHavingOrganization)
	// organization := reflect.New(modelObj.OwnershipType()).Interface().(models.IModel)

	if modelObjHavingOwnership, ok = reflect.New(modelObjHavingOrganization.OrganizationType()).Interface().(models.IHasOwnershipLink); !ok {
		return false, fmt.Errorf("Model %s's organization table does not comform to IHasOwnershipLink", typeString)
	}

	// Get organization's join table name
	organizationJoinTableName := models.GetJoinTableName(modelObjHavingOwnership)

	rolesQuery := strconv.Itoa(int(roles[0]))
	for i := 1; i < len(roles); i++ {
		rolesQuery += "," + strconv.Itoa(int(roles[i]))
	}

	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id AND \"%s\".role IN (%s)", organizationJoinTableName, organizationTableName, organizationJoinTableName,
		organizationJoinTableName, rolesQuery)
	secondJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", organizationJoinTableName, organizationJoinTableName)
	db = db.Table(organizationTableName).Joins(firstJoin).Joins(secondJoin, oid.String())

	organizations, err := models.NewSliceFromDBByType(modelObjHavingOrganization.OrganizationType(), db.Find)
	if err != nil {
		return false, err
	}

	// Check that organizations is not an empty array, and one of the organization has an ID that
	// is specified in
	organizationID := modelObjHavingOrganization.GetOrganizationID()
	for _, org := range organizations {
		if organizationID == nil {
			return false, errors.New("missing organization link ID")

		}
		if org.GetID().String() == organizationID.String() {
			return true, nil
		}
	}

	return false, nil
}

// ---------------------------------------

var onceOrganizationMapper sync.Once
var organizationMapper *OrganizationMapper

// OrganizationMapper is a basic CRUD manager
type OrganizationMapper struct {
}

// SharedOrganizationMapper creats a singleton of Crud object
func SharedOrganizationMapper() *OrganizationMapper {
	onceOrganizationMapper.Do(func() {
		organizationMapper = &OrganizationMapper{}
	})

	return organizationMapper
}

// CreateOne creates an instance of this model based on json and store it in db
// when creating, need to put yourself in OrganizationUser as well.
// Well check this!!
func (mapper *OrganizationMapper) CreateOne(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, modelObj models.IModel) (models.IModel, error) {
	if modelObj.GetID() == nil {
		modelObj.SetID(datatypes.NewUUID())
	}

	// Make sure oid has admin access to this organization
	hasAdminAccess, err := userHasRolesAccessToModelOrg(db, oid, typeString, modelObj, []models.UserRole{models.Admin})
	if err != nil {
		return nil, err
	} else if !hasAdminAccess {
		return nil, errors.New("user does not have admin access to the organization")
	}

	// Now, we create the object
	return createOneWithHooks(createOneCoreOrganization, db, oid, scope, typeString, modelObj)
}

// CreateMany creates an instance of this model based on json and store it in db
func (mapper *OrganizationMapper) CreateMany(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, modelObjs []models.IModel) ([]models.IModel, error) {
	retModels := make([]models.IModel, 0, 20)

	cargo := models.BatchHookCargo{}
	// Before batch inert hookpoint
	if before := models.ModelRegistry[typeString].BeforeInsert; before != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err := before(bhpData); err != nil {
			return nil, err
		}
	}

	for _, modelObj := range modelObjs {
		if modelObj.GetID() == nil {
			modelObj.SetID(datatypes.NewUUID())
		}

		// Make sure oid has admin access to this organization
		hasAdminAccess, err := userHasRolesAccessToModelOrg(db, oid, typeString, modelObj, []models.UserRole{models.Admin})
		if err != nil {
			return nil, err
		} else if !hasAdminAccess {
			return nil, errors.New("user does not have admin access to the organization")
		}

		m, err := createOneCoreOrganization(db, oid, typeString, modelObj)
		if err != nil {
			// That's weird. we have just inserted it.
			return nil, err
		}

		retModels = append(retModels, m)
	}

	// After batch insert hookpoint
	if after := models.ModelRegistry[typeString].AfterInsert; after != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err := after(bhpData); err != nil {
			return nil, err
		}
	}

	return retModels, nil
}

// GetOneWithID get one model object based on its type and its id string
func (mapper *OrganizationMapper) GetOneWithID(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, id datatypes.UUID) (models.IModel, models.UserRole, error) {

	modelObj, role, err := mapper.getOneWithIDCore(db, oid, scope, typeString, id)
	if err != nil {
		return nil, 0, err
	}

	if m, ok := modelObj.(models.IAfterRead); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Role: &role}
		if err := m.AfterReadDB(hpdata); err != nil {
			return nil, 0, err
		}
	}

	return modelObj, role, err
}

// getOneWithIDCore get one model object based on its type and its id string
// since this is organizationMapper, need to make sure it's the same organization
func (mapper *OrganizationMapper) getOneWithIDCore(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, id datatypes.UUID) (models.IModel, models.UserRole, error) {
	modelObj := models.NewFromTypeString(typeString)

	db = db.Set("gorm:auto_preload", true)

	var ok bool
	var modelObjHavingOrganization models.IHasOrganizationLink
	// (Maybe organization should be defined in the library)
	// And it's organizational type has a user which includes
	if modelObjHavingOrganization, ok = models.NewFromTypeString(typeString).(models.IHasOrganizationLink); !ok {
		return nil, models.Guest, fmt.Errorf("Model %s does not comform to IHasOrganizationLink", typeString)
	}

	// Graphically:
	// Model -- Org -- Join Table -- User
	orgTableName := models.GetOrganizationTableName(modelObjHavingOrganization)
	orgTable := reflect.New(modelObjHavingOrganization.OrganizationType()).Interface()
	joinTableName := models.GetJoinTableName(orgTable.(models.IHasOwnershipLink))
	orgFieldName := strcase.SnakeCase(modelObjHavingOrganization.GetOrganizationIDFieldName())

	rtable := models.GetTableNameFromIModel(modelObj)

	// e.g. INNER JOIN \"organization\" ON \"dock\".\"OrganizationID\" = \"organization\".id
	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".\"%s\" = \"%s\".id AND \"%s\".\"id\" = ?", orgTableName, rtable, orgFieldName, orgTableName, rtable)
	// e.g. INNER JOIN \"user_owns_organization\" ON \"organization\".id = \"user_owns_organization\".model_id
	secondJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id", joinTableName, orgTableName, joinTableName)
	thirdJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", joinTableName, joinTableName)

	err := db.Table(rtable).Joins(firstJoin, id.String()).Joins(secondJoin).Joins(thirdJoin, oid.String()).Find(modelObj).Error
	if err != nil {
		return nil, 0, err
	}

	joinTable := reflect.New(orgTable.(models.IHasOwnershipLink).OwnershipType()).Interface()
	role := models.Guest // just some default

	orgID := modelObj.(models.IHasOrganizationLink).GetOrganizationID().String()

	// Get the roles for the organizations this user has access to
	if err2 := db.Table(joinTableName).Select("model_id, role").Where("user_id = ? AND model_id = ?", oid.String(),
		orgID).Scan(joinTable).Error; err2 != nil {
		return nil, 0, err2
	}

	if m, ok := joinTable.(models.IOwnership); ok {
		role = m.GetRole()
	}

	err = loadManyToManyBecauseGormFailsWithID(db, modelObj)
	if err != nil {
		return nil, 0, err
	}

	return modelObj, role, err
}

// GetAll obtains a slice of models.DomainModel
// options can be string "offset" and "limit", both of type int
// This is very Javascript-esque. I would have liked Python's optional parameter more.
// Alas, no such feature in Go. https://stackoverflow.com/questions/2032149/optional-parameters-in-go
// How does Gorm do the following? Might want to check out its source code.
// Cancel offset condition with -1
//  db.Offset(10).Find(&users1).Offset(-1).Find(&users2)
func (mapper *OrganizationMapper) GetAll(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, options map[URLParam]interface{}) ([]models.IModel, []models.UserRole, *int, error) {
	db2 := db
	db = db.Set("gorm:auto_preload", true)

	offset, limit, cstart, cstop, order, latestn, totalcount := getOptions(options)

	var ok bool
	var modelObjHavingOrganization models.IHasOrganizationLink
	// (Maybe organization should be defined in the library)
	// And it's organizational type has a user which includes
	if modelObjHavingOrganization, ok = models.NewFromTypeString(typeString).(models.IHasOrganizationLink); !ok {
		return nil, nil, nil, fmt.Errorf("Model %s does not comform to IHasOrganizationLink", typeString)
	}

	// Graphically:
	// Model -- Org -- Join Table -- User
	orgTableName := models.GetOrganizationTableName(modelObjHavingOrganization)
	orgTable := reflect.New(modelObjHavingOrganization.OrganizationType()).Interface()
	joinTableName := models.GetJoinTableName(orgTable.(models.IHasOwnershipLink))
	orgFieldName := strcase.SnakeCase(modelObjHavingOrganization.GetOrganizationIDFieldName())

	rtable := models.GetTableNameFromTypeString(typeString)

	// e.g. INNER JOIN \"organization\" ON \"dock\".\"OrganizationID\" = \"organization\".id
	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".\"%s\" = \"%s\".id", orgTableName, rtable, orgFieldName, orgTableName)
	// e.g. INNER JOIN \"user_owns_organization\" ON \"organization\".id = \"user_owns_organization\".model_id
	secondJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id", joinTableName, orgTableName, joinTableName)
	thirdJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", joinTableName, joinTableName)

	if cstart != nil && cstop != nil {
		db = db.Where(rtable+".created_at BETWEEN ? AND ?", time.Unix(int64(*cstart), 0), time.Unix(int64(*cstop), 0))
	}

	var err error

	db, err = constructInnerFieldParamQueries(db, typeString, options, latestn)
	if err != nil {
		return nil, nil, nil, err
	}

	db = db.Table(rtable).Joins(firstJoin).Joins(secondJoin).Joins(thirdJoin, oid.String())

	db = constructOrderFieldQueries(db, rtable, order)

	var no *int
	if totalcount {
		no = new(int)
		// Query for total count, without offset and limit (all)
		if err := db.Count(no).Error; err != nil {
			log.Println("count error:", err)
			return nil, nil, nil, err
		}
	}

	if offset != nil && limit != nil {
		db = db.Offset(*offset).Limit(*limit)
	}

	outmodels, err := models.NewSliceFromDBByTypeString(typeString, db.Find) // error from db is returned from here

	// Now we need to fill in roles for each model. With regular ownershipmapper, the link table
	// itself has role values and we need to query that
	// But with OrganizationMapper, the role the user has to the organization is the role the user
	// has to the model. We do the following:
	// 1. We query all the organization this user belongs to, get the roles
	// 2. With the models we have, we find the organization id, and match it to the organization in step 1,
	// then fetch the role, which becomes the role for the model

	// Get the roles for the organizations this user has access to
	// stmt := fmt.Sprintf("SELECT model_id, role FROM %s WHERE user_id = ?", joinTableName)
	rows, err := db2.Table(joinTableName).Select("model_id, role").Where("user_id = ?", oid.String()).Rows()
	if err != nil {
		return nil, nil, nil, err
	}

	thisRole := models.Guest              // just some default
	organizationID := datatypes.NewUUID() // just some default
	orgIDToRoleMap := make(map[string]models.UserRole)
	for rows.Next() {
		if err = rows.Scan(organizationID, &thisRole); err != nil {
			return nil, nil, nil, err
		}

		orgIDToRoleMap[organizationID.String()] = thisRole
	}

	roles := make([]models.UserRole, 0)
	for _, outmodel := range outmodels {
		o := outmodel.(models.IHasOrganizationLink)
		orgID := o.GetOrganizationID().String()

		role := orgIDToRoleMap[orgID]
		roles = append(roles, role)
	}

	// safeguard, Must be coded wrongly
	if len(outmodels) != len(roles) {
		return nil, nil, nil, errors.New("unknown query error")
	}

	// make many to many tag works
	for _, m := range outmodels {
		err = loadManyToManyBecauseGormFailsWithID(db2, m)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	// use db2 cuz it's not chained
	if after := models.ModelRegistry[typeString].AfterRead; after != nil {
		bhpData := models.BatchHookPointData{Ms: outmodels, DB: db2, OID: oid, Scope: scope, TypeString: typeString, Roles: roles}
		if err = after(bhpData); err != nil {
			return nil, nil, nil, err
		}
	}

	return outmodels, roles, no, err
}

// UpdateOneWithID updates model based on this json
func (mapper *OrganizationMapper) UpdateOneWithID(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, modelObj models.IModel, id datatypes.UUID) (models.IModel, error) {
	if err := checkErrorBeforeUpdate(mapper, db, oid, scope, typeString, modelObj, id, models.Admin); err != nil {
		return nil, err
	}

	cargo := models.ModelCargo{}

	// Before hook
	if v, ok := modelObj.(models.IBeforeUpdate); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err := v.BeforeUpdateDB(hpdata); err != nil {
			return nil, err
		}
	}

	modelObj2, err := updateOneCore(mapper, db, oid, scope, typeString, modelObj, id, models.Admin)
	if err != nil {
		return nil, err
	}

	// After hook
	if v, ok := modelObj2.(models.IAfterUpdate); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = v.AfterUpdateDB(hpdata); err != nil {
			return nil, err
		}
	}

	return modelObj2, nil
}

// UpdateMany updates multiple models
func (mapper *OrganizationMapper) UpdateMany(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, modelObjs []models.IModel) ([]models.IModel, error) {
	ms := make([]models.IModel, 0, 0)
	var err error
	cargo := models.BatchHookCargo{}

	// Before batch update hookpoint
	if before := models.ModelRegistry[typeString].BeforeUpdate; before != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = before(bhpData); err != nil {
			return nil, err
		}
	}

	for _, modelObj := range modelObjs {
		id := modelObj.GetID()

		if err = checkErrorBeforeUpdate(mapper, db, oid, scope, typeString, modelObj, *id, models.Admin); err != nil {
			return nil, err
		}

		m, err := updateOneCore(mapper, db, oid, scope, typeString, modelObj, *id, models.Admin)
		if err != nil { // Error is "record not found" when not found
			return nil, err
		}

		ms = append(ms, m)
	}

	// After batch update hookpoint
	if after := models.ModelRegistry[typeString].AfterUpdate; after != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = after(bhpData); err != nil {
			return nil, err
		}
	}

	return ms, nil
}

// PatchOneWithID updates model based on this json
func (mapper *OrganizationMapper) PatchOneWithID(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, jsonPatch []byte, id datatypes.UUID) (models.IModel, error) {
	var modelObj models.IModel
	var err error
	cargo := models.ModelCargo{}
	var role models.UserRole

	// Check id error
	if id.UUID.String() == "" {
		return nil, errIDEmpty
	}

	// role already chcked in checkErrorBeforeUpdate
	if modelObj, role, err = mapper.getOneWithIDCore(db, oid, scope, typeString, id); err != nil {
		return nil, err
	}

	// calling checkErrorBeforeUpdate is redundant in this case since we need to fetch it out first in order to patch it
	// Just check if role matches models.Admin
	if role != models.Admin {
		return nil, errPermission
	}

	// Apply patch operations
	modelObj, err = patchOneCore(typeString, modelObj, jsonPatch)
	if err != nil {
		return nil, err
	}

	// Before hook
	// It is now expected that the hookpoint for before expect that the patch
	// gets applied to the JSON, but not before actually updating to DB.
	if v, ok := modelObj.(models.IBeforePatch); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err := v.BeforePatchDB(hpdata); err != nil {
			return nil, err
		}
	}

	// Now save it
	modelObj2, err := updateOneCore(mapper, db, oid, scope, typeString, modelObj, id, models.Admin)
	if err != nil {
		return nil, err
	}

	// After hook
	if v, ok := modelObj2.(models.IAfterPatch); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = v.AfterPatchDB(hpdata); err != nil {
			return nil, err
		}
	}

	return modelObj2, nil
}

// PatchMany updates models based on JSON
func (mapper *OrganizationMapper) PatchMany(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, jsonIDPatches []models.JSONIDPatch) ([]models.IModel, error) {
	ms := make([]models.IModel, 0, len(jsonIDPatches)) // len(ms)=0, cap(ms)=len(jsonIDPatches)
	// var err error

	// Load data, patch it, then send it to the hookpoint
	// Load IDs
	ids := make([]*datatypes.UUID, len(jsonIDPatches))
	for i, jsonIDPatch := range jsonIDPatches {
		// Check error, make sure it has an id and not empty string (could potentially update all records!)
		if jsonIDPatch.ID.String() == "" {
			return nil, errIDEmpty
		}
		ids[i] = jsonIDPatch.ID
	}

	var ok bool
	var modelObjHavingOrganization models.IHasOrganizationLink
	if modelObjHavingOrganization, ok = models.NewFromTypeString(typeString).(models.IHasOrganizationLink); !ok {
		return nil, fmt.Errorf("Model %s does not comform to IHasOrganizationLink", typeString)
	}

	// Graphically:
	// Model -- Org -- Join Table -- User
	rtable := models.GetTableNameFromTypeString(typeString)
	orgTableName := models.GetOrganizationTableName(modelObjHavingOrganization)
	orgTable := reflect.New(modelObjHavingOrganization.OrganizationType()).Interface()
	joinTableName := models.GetJoinTableName(orgTable.(models.IHasOwnershipLink))
	orgFieldName := strcase.SnakeCase(modelObjHavingOrganization.GetOrganizationIDFieldName())

	// e.g. INNER JOIN \"organization\" ON \"dock\".\"OrganizationID\" = \"organization\".id
	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".\"%s\" = \"%s\".id AND \"%s\".\"id\" IN (?)", orgTableName, rtable, orgFieldName, orgTableName, rtable)
	// e.g. INNER JOIN \"user_owns_organization\" ON \"organization\".id = \"user_owns_organization\".model_id
	secondJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id", joinTableName, orgTableName, joinTableName)
	thirdJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", joinTableName, joinTableName)

	db2 := db.Table(rtable).Joins(firstJoin, ids).Joins(secondJoin).Joins(thirdJoin, oid) // .Find(modelObj).Error
	modelObjs, err := models.NewSliceFromDBByTypeString(typeString, db2.Set("gorm:auto_preload", true).Find)
	if err != nil {
		log.Println("calling NewSliceFromDBByTypeString err:", err)
		return nil, err
	}

	// Just in case err didn't work (as in the case with IN clause NOT in the ID field, maybe Gorm bug)
	if len(modelObjs) == 0 {
		return nil, fmt.Errorf("not found")
	}

	if len(modelObjs) != len(jsonIDPatches) {
		return nil, errBatchUpdateOrPatchOneNotFound
	}

	// Check error
	// Load the roles and check if they're admin
	roles := make([]models.UserRole, 0)
	if err := db2.Select(fmt.Sprintf("\"%s\".\"role\"", joinTableName)).Scan(&roles).Error; err != nil {
		log.Printf("err getting roles")
		return nil, err
	}

	for _, role := range roles {
		if role != models.Admin {
			return nil, errPermission
		}
	}

	// Now patch it
	for i, jsonIDPatch := range jsonIDPatches {
		// Apply patch operations
		modelObjs[i], err = patchOneCore(typeString, modelObjs[i], []byte(jsonIDPatch.Patch))
		if err != nil {
			log.Println("patch error: ", err, string(jsonIDPatch.Patch))
			return nil, err
		}
	}

	cargo := models.BatchHookCargo{}
	// Before batch update hookpoint
	if before := models.ModelRegistry[typeString].BeforeUpdate; before != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = before(bhpData); err != nil {
			return nil, err
		}
	}

	// TODO: Could update all at once, then load all at once again
	for _, modelObj := range modelObjs {
		id := modelObj.GetID()

		m, err := updateOneCore(mapper, db, oid, scope, typeString, modelObj, *id, models.Admin)
		if err != nil { // Error is "record not found" when not found
			return nil, err
		}

		ms = append(ms, m)
	}

	// After batch update hookpoint
	if after := models.ModelRegistry[typeString].AfterUpdate; after != nil {
		bhpData := models.BatchHookPointData{Ms: ms, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = after(bhpData); err != nil {
			return nil, err
		}
	}

	return ms, nil
}

// DeleteOneWithID delete the model
// TODO: delete the groups associated with this record?
func (mapper *OrganizationMapper) DeleteOneWithID(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, id datatypes.UUID) (models.IModel, error) {
	if id.UUID.String() == "" {
		return nil, errIDEmpty
	}

	// check out
	// https://stackoverflow.com/questions/52124137/cant-set-field-of-a-struct-that-is-typed-as-an-interface
	/*
		a := reflect.ValueOf(modelObj).Elem()
		b := reflect.Indirect(a).FieldByName("ID")
		b.Set(reflect.ValueOf(uint(id)))
	*/

	// Pull out entire modelObj
	modelObj, role, err := mapper.getOneWithIDCore(db, oid, scope, typeString, id)
	if err != nil { // Error is "record not found" when not found
		return nil, err
	}
	if role != models.Admin {
		return nil, errPermission
	}

	cargo := models.ModelCargo{}

	// Before delete hookpoint
	if v, ok := modelObj.(models.IBeforeDelete); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		err = v.BeforeDeleteDB(hpdata)
		if err != nil {
			return nil, err
		}
	}

	// Unscoped() for REAL delete!
	// Foreign key constraint works only on real delete
	// Soft delete will take more work, have to verify myself manually
	if modelNeedsRealDelete(modelObj) {
		db = db.Unscoped()
	}

	err = db.Delete(modelObj).Error
	if err != nil {
		return nil, err
	}

	// Remove ownership
	// modelObj.
	// db.Model(modelObj).Association("Ownerships").Delete(modelObj.)
	// c.DB.Model(&user).Association("Roles").Delete(&role)

	err = removePeggedField(db, modelObj)
	if err != nil {
		return nil, err
	}

	// After delete hookpoint
	if v, ok := modelObj.(models.IAfterDelete); ok {
		hpdata := models.HookPointData{DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		err = v.AfterDeleteDB(hpdata)
		if err != nil {
			return nil, err
		}
	}

	return modelObj, nil
}

// DeleteMany deletes multiple models
func (mapper *OrganizationMapper) DeleteMany(db *gorm.DB, oid *datatypes.UUID, scope *string, typeString string, modelObjs []models.IModel) ([]models.IModel, error) {

	ids := make([]datatypes.UUID, len(modelObjs), len(modelObjs))
	var err error
	cargo := models.BatchHookCargo{}
	for i, v := range modelObjs {
		id := v.GetID()
		if id.String() == "" {
			return nil, errIDEmpty
		}

		ids[i] = *id
	}

	ms := make([]models.IModel, 0, 0)

	// Before batch delete hookpoint
	if before := models.ModelRegistry[typeString].BeforeDelete; before != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = before(bhpData); err != nil {
			return nil, err
		}
	}

	for i, id := range ids {

		if id.UUID.String() == "" {
			return nil, errIDEmpty
		}

		// Pull out entire modelObj
		modelObj, role, err := mapper.getOneWithIDCore(db, oid, scope, typeString, id)
		if err != nil { // Error is "record not found" when not found
			return nil, err
		}
		if role != models.Admin {
			return nil, errPermission
		}

		// Unscoped() for REAL delete!
		// Foreign key constraint works only on real delete
		// Soft delete will take more work, have to verify myself manually
		if modelNeedsRealDelete(modelObj) && i == 0 { // only do once
			db = db.Unscoped()
		}

		err = db.Delete(modelObj).Error
		// err = db.Delete(modelObj).Error
		if err != nil {
			return nil, err
		}

		err = removePeggedField(db, modelObj)
		if err != nil {
			return nil, err
		}

		ms = append(ms, modelObj)
	}

	// After batch delete hookpoint
	if after := models.ModelRegistry[typeString].AfterDelete; after != nil {
		bhpData := models.BatchHookPointData{Ms: modelObjs, DB: db, OID: oid, Scope: scope, TypeString: typeString, Cargo: &cargo}
		if err = after(bhpData); err != nil {
			return nil, err
		}
	}

	return ms, nil
}
