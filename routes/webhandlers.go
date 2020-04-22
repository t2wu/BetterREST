package routes

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/t2wu/betterrest/datamapper"
	"github.com/t2wu/betterrest/db"
	"github.com/t2wu/betterrest/libs/security"
	"github.com/t2wu/betterrest/models"
	"github.com/t2wu/betterrest/models/tools"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/render"
	"github.com/jinzhu/gorm"
)

// ---------------------------------------------
func limitAndOffsetFromQueryString(w http.ResponseWriter, r *http.Request) (int, int, error) {
	// Can I do the following in one statement?
	if offset, limit := r.URL.Query().Get("offset"), r.URL.Query().Get("limit"); offset != "" && limit != "" {
		var o, l int
		var err error
		if o, err = strconv.Atoi(offset); err != nil {
			return 0, 0, err
		}
		if l, err = strconv.Atoi(limit); err != nil {
			return 0, 0, err
		}
		return o, l, nil
	}
	return 0, 0, nil // It's ok to pass 0 limit, it'll be interpreted as an all.
}

func createdTimeRangeFromQueryString(w http.ResponseWriter, r *http.Request) (int, int, error) {
	cstart, cstop := r.URL.Query().Get("cstart"), r.URL.Query().Get("cstop")

	if cstart == "" && cstop == "" { // not specified at all
		return 0, 0, nil
	}

	var err error
	cStartInt, cStopInt := 0, 0
	if cstart != "" {
		if cStartInt, err = strconv.Atoi(cstart); err != nil {
			return 0, 0, err
		}
	} else {
		cStartInt = 0
	}

	if cstop != "" {
		if cStopInt, err = strconv.Atoi(cstop); err != nil {
			return 0, 0, err
		}
	} else {
		cStopInt = int(time.Now().Unix()) // now
	}

	return cStartInt, cStopInt, nil
}

func modelObjsToJSON(typeString string, modelObjs []models.IModel) (string, error) {

	arr := make([]string, len(modelObjs))
	for i, v := range modelObjs {
		if j, err := tools.ToJSON(typeString, v, models.Admin); err != nil {
			return "", err
		} else {
			arr[i] = string(j)
		}
	}

	content := "[" + strings.Join(arr, ",\n") + "]"
	return content, nil
}

func renderModel(w http.ResponseWriter, r *http.Request, typeString string, modelObj models.IModel) {
	// render.JSON(w, r, modelObj) // cannot use this since no picking the field we need
	jsonBytes, err := tools.ToJSON(typeString, modelObj, models.Admin)
	if err != nil {
		log.Println("Error in renderModel:", err)
		render.Render(w, r, NewErrGenJSON(err))
		return
	}

	content := fmt.Sprintf("{ \"code\": 0, \"content\": %s }", string(jsonBytes))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(content))
}

func renderModelSlice(w http.ResponseWriter, r *http.Request, typeString string, modelObjs []models.IModel) {
	jsonString, err := modelObjsToJSON(typeString, modelObjs)
	if err != nil {
		log.Println("Error in renderModelSlice:", err)
		render.Render(w, r, NewErrGenJSON(err))
		return
	}

	content := fmt.Sprintf("{ \"code\": 0, \"content\": %s }", jsonString)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write([]byte(content))
}

// ---------------------------------------------

// getVerifiedAuthUser authenticates the user
// getVerifiedAuthUser authenticates the user
func getVerifiedAuthUser(userModel models.IModel) (models.IModel, bool) {
	userModel2 := reflect.New(models.UserTyp).Interface().(models.IModel)

	// TODO: maybe email is not the login, make it more flexible?
	email := reflect.ValueOf(userModel).Elem().FieldByName(("Email")).Interface().(string)
	password := reflect.ValueOf(userModel).Elem().FieldByName(("Password")).Interface().(string)

	err := db.Shared().Where("email = ?", email).First(userModel2).Error
	if gorm.IsRecordNotFoundError(err) {
		return nil, false // User doesn't exists with this email
	} else if err != nil {
		// Some other unknown error
		return nil, false
	}

	passwordHash := reflect.ValueOf(userModel2).Elem().FieldByName("PasswordHash").Interface().(string)
	if !security.IsSamePassword(password, passwordHash) {
		// Password doesn't match
		return nil, false
	}

	return userModel2, true
}

// ---------------------------------------------

// UserLoginHandler logs in the user. Effectively creates a JWT token for the user
func UserLoginHandler() func(c *gin.Context) {
	return func(c *gin.Context) {
		w, r := c.Writer, c.Request
		log.Println("UserLoginHandler")

		m, httperr := ModelFromJSONBody(r, "users") // m is models.IModel
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		authUser, authorized := getVerifiedAuthUser(m)
		if !authorized {
			// unable to login user. maybe doesn't exist?
			// or username, password wrong
			render.Render(w, r, NewErrLoginUser(nil))
			return
		}

		// login success, return access token
		scope := "owner"
		payload, err := createTokenPayloadForScope(authUser.GetID(), &scope)
		if err != nil {
			render.Render(w, r, NewErrGeneratingToken(err))
			return
		}

		var jsn []byte
		if jsn, err = json.Marshal(payload); err != nil {
			render.Render(w, r, NewErrGenJSON(err))
			return
		}

		w.Write(jsn)
	}
}

// ---------------------------------------------
// reflection stuff
// https://stackoverflow.com/questions/7850140/how-do-you-create-a-new-instance-of-a-struct-from-its-type-at-run-time-in-go
// https://stackoverflow.com/questions/23030884/is-there-a-way-to-create-an-instance-of-a-struct-from-a-string

// ReadAllHandler returns a Gin handler which fetch multiple records of a resource
func ReadAllHandler(typeString string, mapper datamapper.IGetAllMapper) func(c *gin.Context) {
	return func(c *gin.Context) {
		w, r := c.Writer, c.Request

		options := make(map[string]interface{})
		var err error
		var modelObjs []models.IModel

		if o, l, err := limitAndOffsetFromQueryString(w, r); err == nil {
			options["offset"], options["limit"] = o, l
		} else if err != nil {
			render.Render(w, r, NewErrQueryParameter(err))
			return
		}

		if cstart, cstop, err := createdTimeRangeFromQueryString(w, r); err == nil {
			options["cstart"], options["cstop"] = cstart, cstop
		} else if err != nil {
			render.Render(w, r, NewErrQueryParameter(err))
			return
		}

		options["fieldName"], options["fieldValue"] = r.URL.Query().Get("fieldName"), r.URL.Query().Get("fieldValue")

		tx := db.Shared().Begin()
		if modelObjs, err = mapper.ReadAll(tx, OwnerIDFromContext(r), typeString, options); err != nil {
			tx.Rollback()
			log.Println("Error in ReadAllHandler ErrNotFound:", typeString, err)
			render.Render(w, r, NewErrNotFound(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in ReadAllHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModelSlice(w, r, typeString, modelObjs)
		return
	}
}

// CreateOneHandler creates a resource
func CreateOneHandler(typeString string, mapper datamapper.ICreateOneMapper) func(c *gin.Context) {
	return func(c *gin.Context) {
		w, r := c.Writer, c.Request

		ownerID := OwnerIDFromContext(r)

		var err error

		modelObj, httperr := ModelFromJSONBody(r, typeString)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		tx := db.Shared().Begin()
		if modelObj, err = mapper.CreateOne(tx, ownerID, typeString, modelObj); err != nil {
			// FIXME, there is more than one type of error here
			// How do I output more detailed messages by inspecting error?
			tx.Rollback()
			log.Println("Error in CreateOne ErrCreate:", typeString, err)
			render.Render(w, r, NewErrCreate(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in CreateOne ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModel(w, r, typeString, modelObj)
		return
	}
}

// ReadOneHandler returns a http.Handler which read one resource
func ReadOneHandler(typeString string, mapper datamapper.IGetOneWithIDMapper) func(c *gin.Context) {
	// return func(next http.Handler) http.Handler {
	return func(c *gin.Context) {
		w, r := c.Writer, c.Request
		log.Println("ReadOneHandler")

		id, httperr := IDFromURLQueryString(c)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		tx := db.Shared().Begin()
		modelObj, err := mapper.GetOneWithID(tx, OwnerIDFromContext(r), typeString, *id)

		if err != nil {
			tx.Rollback()
			log.Println("Error in ReadOneHandler ErrNotFound:", typeString, err)
			render.Render(w, r, NewErrNotFound(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in ReadOneHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModel(w, r, typeString, modelObj)
		return
	}
}

// UpdateOneHandler returns a http.Handler which updates a resource
func UpdateOneHandler(typeString string, mapper datamapper.IUpdateOneWithIDMapper) func(c *gin.Context) {
	return func(c *gin.Context) {
		w, r := c.Writer, c.Request
		// var err error
		// var model models.DomainModel

		log.Println("UpdateOneHandler called")

		test := c.Param("test")
		log.Println("test:", test == "")

		id, httperr := IDFromURLQueryString(c)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		modelObj, httperr := ModelFromJSONBody(r, typeString)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		tx := db.Shared().Begin()
		modelObj, err := mapper.UpdateOneWithID(tx, OwnerIDFromContext(r), typeString, modelObj, *id)
		if err != nil {
			tx.Rollback()
			log.Println("Error in UpdateOneHandler ErrUpdate:", typeString, err)
			render.Render(w, r, NewErrUpdate(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in UpdateOneHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModel(w, r, typeString, modelObj)
		return
	}
}

// UpdateManyHandler returns a Gin handler which updates many records
func UpdateManyHandler(typeString string, mapper datamapper.IUpdateManyMapper) func(c *gin.Context) {
	return func(c *gin.Context) {
		log.Println("UpdateManyHandler called")
		w, r := c.Writer, c.Request

		modelObjs, httperr := ModelsFromJSONBody(r, typeString)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		tx := db.Shared().Begin()
		modelObjs, err := mapper.UpdateMany(tx, OwnerIDFromContext(r), typeString, modelObjs)
		if err != nil {
			tx.Rollback()
			log.Println("Error in UpdateManyHandler ErrUpdate:", typeString, err)
			render.Render(w, r, NewErrUpdate(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in UpdateManyHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModelSlice(w, r, typeString, modelObjs)
		return
	}
}

// PatchOneHandler returns a Gin handler which patch (partial update) one record
func PatchOneHandler(typeString string, mapper datamapper.IPatchOneWithIDMapper) func(c *gin.Context) {
	return func(c *gin.Context) {
		log.Println("PatchOneHandler")
		w, r := c.Writer, c.Request

		id, httperr := IDFromURLQueryString(c)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		var jsonPatch []byte
		var err error
		if jsonPatch, err = ioutil.ReadAll(r.Body); err != nil {
			render.Render(w, r, NewErrReadingBody(err))
			return
		}

		tx := db.Shared().Begin()
		modelObj, err := mapper.PatchOneWithID(tx, OwnerIDFromContext(r), typeString, jsonPatch, *id)
		if err != nil {
			tx.Rollback()
			log.Println("Error in UpdateOneHandler ErrUpdate:", typeString, err)
			render.Render(w, r, NewErrPatch(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in UpdateOneHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModel(w, r, typeString, modelObj)
		return

		// type JSONPatch struct {
		// 	Op    string
		// 	Path  string
		// 	Value interface{}
		// }
	}
}

// DeleteOneHandler returns a Gin handler which delete one record
func DeleteOneHandler(typeString string, mapper datamapper.IDeleteOneWithID) func(c *gin.Context) {
	return func(c *gin.Context) {
		log.Println("DeleteOneHandler")
		w, r := c.Writer, c.Request

		id, httperr := IDFromURLQueryString(c)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		tx := db.Shared().Begin()
		modelObj, err := mapper.DeleteOneWithID(tx, OwnerIDFromContext(r), typeString, *id)
		if err != nil {
			tx.Rollback()
			log.Println("Error in DeleteOneHandler ErrDelete:", typeString, err)
			render.Render(w, r, NewErrDelete(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in DeleteOneHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModel(w, r, typeString, modelObj)
		return
	}
}

// DeleteManyHandler returns a Gin handler which delete many records
func DeleteManyHandler(typeString string, mapper datamapper.IDeleteMany) func(c *gin.Context) {
	return func(c *gin.Context) {
		w, r := c.Writer, c.Request
		log.Println("DeleteManyHandler called")
		var err error

		modelObjs, httperr := ModelsFromJSONBody(r, typeString)
		if httperr != nil {
			render.Render(w, r, httperr)
			return
		}

		tx := db.Shared().Begin() // transaction
		modelObjs, err = mapper.DeleteMany(tx, OwnerIDFromContext(r), typeString, modelObjs)
		if err != nil {
			tx.Rollback()
			log.Println("Error in DeleteOneHandler ErrDelete:", typeString, err)
			render.Render(w, r, NewErrDelete(err))
			return
		}
		if tx.Commit().Error != nil {
			log.Println("Error in DeleteOneHandler ErrDBError:", typeString, tx.Commit().Error)
			render.Render(w, r, NewErrDBError(tx.Commit().Error))
			return
		}

		renderModelSlice(w, r, typeString, modelObjs)
		return
	}
}
