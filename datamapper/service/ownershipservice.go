package service

import (
	"fmt"
	"log"
	"reflect"

	"github.com/jinzhu/gorm"
	"github.com/t2wu/betterrest/datamapper/gormfixes"
	"github.com/t2wu/betterrest/libs/datatypes"
	"github.com/t2wu/betterrest/models"
)

// check out
// https://stackoverflow.com/questions/52124137/cant-set-field-of-a-struct-that-is-typed-as-an-interface
/*
	a := reflect.ValueOf(modelObj).Elem()
	b := reflect.Indirect(a).FieldByName("ID")
	b.Set(reflect.ValueOf(uint(id)))
*/

// OwnershipService handles all the ownership specific db calls
type OwnershipService struct {
	BaseService
}

func (serv *OwnershipService) HookBeforeCreateOne(db *gorm.DB, who models.Who, typeString string, modelObj models.IModel) (models.IModel, error) {
	modelID := modelObj.GetID()
	if modelID == nil {
		modelID = datatypes.NewUUID()
		modelObj.SetID(modelID)
	}

	g := models.NewOwnershipModelFromOwnershipResourceTypeString(typeString).(models.IOwnership)
	g.SetUserID(who.Oid)
	g.SetModelID(modelID)
	g.SetRole(models.UserRoleAdmin)

	// ownerships := reflect.New(reflect.SliceOf(ownershipType))
	// o.Set(reflect.Append(ownerships, reflect.ValueOf(g)))

	// Associate a ownership group with this model
	// This is not strictly really necessary as actual SQL table has no such field. I could have
	// just save the "g", But it's for hooks
	o := reflect.ValueOf(modelObj).Elem().FieldByName("Ownerships")
	o.Set(reflect.Append(o, reflect.ValueOf(g).Elem()))
	return modelObj, nil
}

func (serv *OwnershipService) HookBeforeCreateMany(db *gorm.DB, who models.Who, typeString string, modelObjs []models.IModel) ([]models.IModel, error) {
	for _, modelObj := range modelObjs {
		// reflect.SliceOf
		// g := reflect.New(ownershipType).Interface().(models.IOwnership)
		g := models.NewOwnershipModelFromOwnershipResourceTypeString(typeString).(models.IOwnership)

		modelID := modelObj.GetID()
		if modelID == nil {
			modelID = datatypes.NewUUID()
			modelObj.SetID(modelID)
		}

		g.SetUserID(who.Oid)
		g.SetModelID(modelID)
		g.SetRole(models.UserRoleAdmin)

		// ownerships := reflect.New(reflect.SliceOf(ownershipType))
		// o.Set(reflect.Append(ownerships, reflect.ValueOf(g)))

		// Associate a ownership group with this model
		// This is not strictly really necessary as actual SQL table has no such field. I could have
		// just save the "g", But it's for hooks
		o := reflect.ValueOf(modelObj).Elem().FieldByName("Ownerships")
		o.Set(reflect.Append(o, reflect.ValueOf(g).Elem()))
	}
	return modelObjs, nil
}

func (serv *OwnershipService) HookBeforeDeleteOne(db *gorm.DB, who models.Who, typeString string, modelObj models.IModel) (models.IModel, error) {
	// I'm removing stuffs from this link table, I cannot just remove myself from this. I have to remove
	// everyone who is linked to this table!

	// stmt := fmt.Sprintf("DELETE FROM %s WHERE user_id = ? AND model_id = ? AND role = ?", models.GetJoinTableName(modelObjOwnership))
	tableName := models.OwnershipTableNameFromOwnershipResourceTypeString(typeString)
	stmt := fmt.Sprintf("DELETE FROM %s WHERE model_id = ?", tableName)

	// Can't do db.Raw and db.Delete at the same time?!
	db2 := db.Exec(stmt, modelObj.GetID().String())
	if err := db2.Error; err != nil {
		return nil, err
	}

	return modelObj, nil
}

// HookBeforeDeleteMany deletes link table because GORM isn't automatic here when we customize
// it with UUID or when we have role
func (serv *OwnershipService) HookBeforeDeleteMany(db *gorm.DB, who models.Who, typeString string, modelObjs []models.IModel) ([]models.IModel, error) {
	for _, modelObj := range modelObjs {
		// Also remove entries from ownership table
		// Maybe getting table
		tableName := models.OwnershipTableNameFromOwnershipResourceTypeString(typeString)
		stmt := fmt.Sprintf("DELETE FROM %s WHERE model_id = ?", tableName)
		db2 := db.Exec(stmt, modelObj.GetID().String())
		if err := db2.Error; err != nil {
			return nil, err
		}
	}
	return modelObjs, nil
}

// CreateOneCore creates the stuff
func (serv *OwnershipService) CreateOneCore(db *gorm.DB, who models.Who, typeString string, modelObj models.IModel, id *datatypes.UUID, oldModelObj models.IModel) (models.IModel, error) {
	// It looks like I need to explicitly call create here
	o := reflect.ValueOf(modelObj).Elem().FieldByName("Ownerships")
	g, _ := o.Index(0).Addr().Interface().(models.IOwnership)

	// No need to check if primary key is blank.
	// If it is it'll be created by Gorm's BeforeCreate hook
	// (defined in base model)
	// if dbc := db.Create(modelObj); dbc.Error != nil {
	if err := db.Create(modelObj).Error; err != nil {
		return nil, err
	}

	// Create ownership table
	tableName := models.OwnershipTableNameFromOwnershipResourceTypeString(typeString)
	if err := db.Table(tableName).Create(g).Error; err != nil {
		return nil, err
	}

	// For pegassociated, the since we expect association_autoupdate:false
	// need to manually create it
	if err := gormfixes.CreatePeggedAssocFields(db, modelObj); err != nil {
		return nil, err
	}

	// For table with trigger which update before insert, we need to load it again
	if err := db.First(modelObj).Error; err != nil {
		// That's weird. we just inserted it.
		return nil, err
	}

	return modelObj, nil
}

// GetOneWithIDCore get one model object based on its type and its id string
func (serv *OwnershipService) GetOneWithIDCore(db *gorm.DB, who models.Who, typeString string, id *datatypes.UUID) (models.IModel, models.UserRole, error) {
	modelObj := models.NewFromTypeString(typeString)

	db = db.Set("gorm:auto_preload", true)

	rtable := models.GetTableNameFromIModel(modelObj)

	/*
		SELECT * from some_model
		INNER JOIN user_owns_somemodel ON somemodel.id = user_owns_somemodel.model_id AND somemodel.id = UUID_TO_BIN(id)
		INNER JOIN user ON user.id = user_owns_somemodel.user_id AND user.id = UUID_TO_BIN(oid)
	*/

	joinTableName := models.OwnershipTableNameFromOwnershipResourceTypeString(typeString)
	log.Println("joinTableName???", joinTableName)
	// joinTableName := models.GetJoinTableName(modelObj)

	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id AND \"%s\".id = ?", joinTableName, rtable, joinTableName, rtable)
	secondJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", joinTableName, joinTableName)
	if err := db.Model(modelObj).Joins(firstJoin, id.String()).Joins(secondJoin, who.Oid.String()).Find(modelObj).Error; err != nil {
		return nil, 0, err
	}

	if err := gormfixes.LoadManyToManyBecauseGormFailsWithID(db, modelObj); err != nil {
		return nil, 0, err
	}

	// joinTableName = "user_owns_" + rtable
	res := struct {
		Role models.UserRole
	}{Role: models.UserRoleInvalid}
	if err := db.Table(joinTableName).Where("user_id = ? AND model_id = ?",
		who.Oid.String(), id.String()).Scan(&res).Error; err != nil {
		return nil, 0, err
	}

	return modelObj, res.Role, nil
}

// GetManyWithIDsCore -
func (serv *OwnershipService) GetManyWithIDsCore(db *gorm.DB, who models.Who, typeString string, ids []*datatypes.UUID) ([]models.IModel, []models.UserRole, error) {
	// If I can load it, I have permission to edit it. So no need to call loadAndCheckErrorBeforeModify
	// like when I do for update. Just get the role and check if it's admin
	rtable, joinTableName, err := getModelTableNameAndJoinTableNameFromTypeString(typeString)
	if err != nil {
		return nil, nil, err
	}

	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id AND \"%s\".id IN (?)", joinTableName, rtable, joinTableName, rtable)
	secondJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", joinTableName, joinTableName)

	db2 := db.Table(rtable).Joins(firstJoin, ids).Joins(secondJoin, who.Oid)
	modelObjs, err := models.NewSliceFromDBByTypeString(typeString, db2.Set("gorm:auto_preload", true).Find)
	if err != nil {
		log.Println("calling NewSliceFromDBByTypeString err:", err)
		return nil, nil, err
	}

	// Just in case err didn't work (as in the case with IN clause NOT in the ID field, maybe Gorm bug)
	if len(modelObjs) == 0 {
		return nil, nil, fmt.Errorf("not found")
	}

	if len(modelObjs) != len(ids) {
		return nil, nil, ErrBatchUpdateOrPatchOneNotFound
	}

	// Check error
	// Load the roles and check if they're admin
	roles := make([]models.UserRole, 0)
	if err := db2.Select(fmt.Sprintf("\"%s\".\"role\"", joinTableName)).Scan(&roles).Error; err != nil {
		log.Printf("err getting roles")
		return nil, nil, err
	}

	for _, modelObj := range modelObjs {
		err = gormfixes.LoadManyToManyBecauseGormFailsWithID(db, modelObj)
		if err != nil {
			return nil, nil, err
		}
	}

	return modelObjs, roles, nil
}

// GetAllQueryContructCore construct the meat of the query
func (serv *OwnershipService) GetAllQueryContructCore(db *gorm.DB, who models.Who, typeString string) (*gorm.DB, error) {
	rtable, joinTableName, err := getModelTableNameAndJoinTableNameFromTypeString(typeString)
	if err != nil {
		return nil, err
	}

	firstJoin := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".id = \"%s\".model_id", joinTableName, rtable, joinTableName)
	secondJoin := fmt.Sprintf("INNER JOIN \"user\" ON \"user\".id = \"%s\".user_id AND \"%s\".user_id = ?", joinTableName, joinTableName)
	db = db.Table(rtable).Joins(firstJoin).Joins(secondJoin, who.Oid.String())

	return db, nil
}

// GetAllRolesCore gets all roles according to the criteria
func (serv *OwnershipService) GetAllRolesCore(dbChained *gorm.DB, dbClean *gorm.DB, who models.Who, typeString string, modelObjs []models.IModel) ([]models.UserRole, error) {
	rtable, joinTableName, err := getModelTableNameAndJoinTableNameFromTypeString(typeString)
	if err != nil {
		return nil, err
	}

	roles := make([]models.UserRole, 0)

	// ---------------------------
	// ownershipModelTyp := getOwnershipModelTypeFromTypeString(typeString)

	// role := models.UserRoleAdmin // just some default
	// The difference between this method and the find is that it's missing the
	// WHERE "model"."deleted_at" IS NULL, so we need to add it
	if err = dbChained.Where(fmt.Sprintf("\"%s\".\"deleted_at\" IS NULL", rtable)).
		Select(fmt.Sprintf("\"%s\".\"role\"", joinTableName)).Scan(&roles).Error; err != nil {
		return nil, err
	}

	return roles, nil
}

// UpdateOneCore one, permissin should already be checked
// called for patch operation as well (after patch has already applied)
// Fuck, repeat the following code for now (you can't call the overriding method from the non-overriding one)
func (serv *OwnershipService) UpdateOneCore(db *gorm.DB, who models.Who, typeString string, modelObj models.IModel, id *datatypes.UUID, oldModelObj models.IModel) (modelObj2 models.IModel, err error) {
	if modelNeedsRealDelete(oldModelObj) { // parent model
		db = db.Unscoped()
	}

	err = gormfixes.UpdatePeggedFields(db, oldModelObj, modelObj)
	if err != nil {
		return nil, err
	}

	// For some unknown reason
	// insert many-to-many works cuz Gorm does and works???
	// [2020-05-22 18:50:17]  [1.63ms]  INSERT INTO \"dock_group\" (\"group_id\",\"dock_id\") SELECT '<binary>','<binary>' FROM DUAL WHERE NOT EXISTS (SELECT * FROM \"dock_group\" WHERE \"group_id\" = '<binary>' AND \"dock_id\" = '<binary>')
	// [0 rows affected or returned ]

	// (/Users/t2wu/Documents/Go/pkg/mod/github.com/t2wu/betterrest@v0.1.19/datamapper/modulelibs.go:62)
	// [2020-05-22 18:50:17]  [1.30ms]  UPDATE \"dock\" SET \"updated_at\" = '2020-05-22 18:50:17', \"deleted_at\" = NULL, \"name\" = '', \"model\" = '', \"serial_no\" = '', \"mac\" = '', \"hub_id\" = NULL, \"is_online\" = false, \"room_id\" = NULL  WHERE \"dock\".\"deleted_at\" IS NULL AND \"dock\".\"id\" = '{2920e86e-33b1-4848-a773-e68e5bde4fc0}'
	// [1 rows affected or returned ]

	// (/Users/t2wu/Documents/Go/pkg/mod/github.com/t2wu/betterrest@v0.1.19/datamapper/modulelibs.go:62)
	// [2020-05-22 18:50:17]  [2.84ms]  INSERT INTO \"dock_group\" (\"dock_id\",\"group_id\") SELECT ') �n3�HH�s�[�O�','<binary>' FROM DUAL WHERE NOT EXISTS (SELECT * FROM \"dock_group\" WHERE \"dock_id\" = ') �n3�HH�s�[�O�' AND \"group_id\" = '<binary>')
	// [1 rows affected or returned ]
	if err = db.Save(modelObj).Error; err != nil { // save updates all fields (FIXME: need to check for required)
		log.Println("Error updating:", err)
		return nil, err
	}

	// This loads the IDs
	// This so we have the preloading.
	modelObj2, _, err = serv.GetOneWithIDCore(db, who, typeString, id)
	if err != nil { // Error is "record not found" when not found
		log.Println("Error:", err)
		return nil, err
	}

	// ouch! for many to many we need to remove it again!!
	// because it's in a transaction so it will load up again
	gormfixes.FixManyToMany(modelObj, modelObj2)

	return modelObj2, nil
}
