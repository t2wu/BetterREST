package sqlbuilder

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/jinzhu/gorm"
	"github.com/stoewer/go-strcase"
	"github.com/t2wu/betterrest/libs/datatypes"
	"github.com/t2wu/betterrest/libs/utils/letters"
	"github.com/t2wu/betterrest/models"
)

// Something like this.
// Search by dense_rank

// FilterCriteria is the criteria to query for first-level field
type FilterCriteria struct {
	// TableName   string
	FieldName   string   // Field name to match
	FieldValues []string // Criteria to match
}

// TwoLevelFilterCriteria is the criteria to query for inner level field
type TwoLevelFilterCriteria struct { //看到I看不到 lower left bracket
	OuterTableName string
	InnerTableName string
	OuterFieldName string
	Filters        []FilterCriteria // Key: inner table name,
}

// AddWhereStmt adds where statement into db
func AddWhereStmt(db *gorm.DB, typeString string, tableName string, filter FilterCriteria) (*gorm.DB, error) {
	modelObj := models.NewFromTypeString(typeString)
	transformedFieldValues, err := getTransformedValueFromValidField(modelObj,
		letters.CamelCaseToPascalCase(filter.FieldName), filter.FieldValues)
	if err != nil {
		return nil, err
	}

	fiterdFieldValues, anyNull := filterNullValue(transformedFieldValues)

	// Gorm will actually use one WHERE clause with AND statements if Where is called repeatedly
	whereStmt := inOpWithFields(tableName, strcase.SnakeCase(filter.FieldName),
		len(fiterdFieldValues), anyNull)

	db = db.Where(whereStmt, fiterdFieldValues...)
	return db, nil
}

// AddNestedQueryJoinStmt adds a join statement into db
func AddNestedQueryJoinStmt(db *gorm.DB, typeString string, criteria TwoLevelFilterCriteria) (*gorm.DB, error) {
	// join inner table and outer table based on outer table id
	joinStmt := fmt.Sprintf("INNER JOIN \"%s\" ON \"%s\".%s = \"%s\".id ",
		criteria.InnerTableName, criteria.InnerTableName, criteria.OuterTableName+"_id", criteria.OuterTableName)

	queryValues := make([]interface{}, 0)
	// var err error

	for _, filter := range criteria.Filters {
		innerFieldName := filter.FieldName
		fieldValues := filter.FieldValues

		// Get inner field type
		m := models.NewFromTypeString(typeString) // THIS IS TO BE FIXED
		fieldType, err := datatypes.GetModelFieldTypeElmIfValid(m, letters.CamelCaseToPascalCase(criteria.OuterFieldName))
		if err != nil {
			return nil, err
		}

		m2 := reflect.New(fieldType).Interface()
		fieldType2, err := datatypes.GetModelFieldTypeElmIfValid(m2, letters.CamelCaseToPascalCase(innerFieldName))
		if err != nil {
			return nil, err
		}

		transformedValues, err := datatypes.TransformFieldValue(fieldType2.String(), fieldValues)
		if err != nil {
			return nil, err
		}

		fiterdFieldValues, anyNull := filterNullValue(transformedValues)

		// It's possible to have multiple values by using ?xx=yy&xx=zz
		// Get the inner table's type
		inStmt := inOpWithFields(criteria.InnerTableName, strcase.SnakeCase(innerFieldName), len(fiterdFieldValues), anyNull)
		joinStmt += "AND (" + inStmt + ")"

		queryValues = append(queryValues, fiterdFieldValues...)
	}

	db = db.Joins(joinStmt, queryValues...)

	return db, nil
}

// AddLatestJoinWithOneLevelFilter generates latest join with one-level filter
// TODO? Can tablename be part of the "?"
func AddLatestJoinWithOneLevelFilter(db *gorm.DB, typeString string, tableName string, latestn int, filters []FilterCriteria) (*gorm.DB, error) {
	partitionByArr := make([]string, 0)
	whereArr := make([]string, 0)

	transformedValues := make([]interface{}, 0)

	for _, filter := range filters {
		m := models.NewFromTypeString(typeString)
		transformedFieldValues, err := getTransformedValueFromValidField(m,
			letters.CamelCaseToPascalCase(filter.FieldName), filter.FieldValues)
		if err != nil {
			return nil, err
		}

		fiterdFieldValues, anyNull := filterNullValue(transformedFieldValues)
		// If passed, the field is part of the data structure

		fieldName := strcase.SnakeCase(filter.FieldName)
		partitionByArr = append(partitionByArr, fieldName)

		whereArr = append(whereArr, inOpWithFields(tableName, fieldName, len(fiterdFieldValues), anyNull)) // "%s.%s IN (%s)
		transformedValues = append(transformedValues, fiterdFieldValues...)
	}
	partitionBy := strings.Join(partitionByArr, ", ")
	whereStmt := strings.Join(whereArr, " AND ")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("INNER JOIN (SELECT id, DENSE_RANK() OVER (PARTITION by %s ORDER BY created_at DESC) FROM %s WHERE %s) AS latestn ",
		partitionBy, tableName, whereStmt)) // WHERE fieldName = fieldValue
	sb.WriteString(fmt.Sprintf("ON %s.id = latestn.id AND latestn.dense_rank <= ?", tableName))
	stmt := sb.String()

	transformedValues = append(transformedValues, latestn)

	db = db.Joins(stmt, transformedValues...)
	return db, nil
}

// AddLatestJoinWithTwoLevelFilter generates latest join with two-level filter
// TODO? Can tablename be part of the "?"
// func AddLatestJoinWithTwoLevelFilter(db *gorm.DB, typeString string, tableName string, latestn int, filter FilterCriteria) {
// 	var sb strings.Builder
// 	sb.WriteString(fmt.Sprintf("INNER JOIN (SELECT %s, DENSE_RANK() OVER (PARTITION by %s ORDER BY created_at DESC) FROM %s) AS latestn ",
// 		tableName, filter.FieldName, tableName))
// 	sb.WriteString(fmt.Sprintf("ON %s.%s = latestn.%s AND %s.created_at = latestn.created_at AND ",
// 		tableName, filter.FieldName, filter.FieldName, tableName))
// 	sb.WriteString(fmt.Sprintf("latestn.dense_rank <= ?"))
// 	stmt := sb.String()
// }

// ===========================================
// Private
// ===========================================

// inOpWithFields generates statements
// xxx IN (?, ?, ?)
// and
// (x1, x2, x3)
// from better_other_queries
func inOpWithFields(tableName string, fieldName string, numfieldValues int, checkNull bool) string {
	tableName = "\"" + tableName + "\""
	fieldName = "\"" + fieldName + "\""
	tableAndField := fmt.Sprintf("%s.%s", tableName, fieldName)

	// A simple IN clause is OK except when I need to check if the field is an null value
	// then the IN clause won't work, need to do
	// (fieldName IN ('fieldValue1', 'fieldValue2') OR fieldName IS NULL)

	var stmt strings.Builder
	if numfieldValues >= 1 {
		questionMarks := strings.Repeat("?,", numfieldValues)
		questionMarks = questionMarks[:len(questionMarks)-1]
		stmt.WriteString(fmt.Sprintf("%s IN (%s)", tableAndField, questionMarks))
	}

	if numfieldValues >= 1 && checkNull {
		stmt.WriteString(" OR ")
	}

	if checkNull {
		stmt.WriteString(fmt.Sprintf("%s IS NULL", tableAndField))
	}

	return stmt.String()
}

// getTransformedValueFromValidField make sure the field does exist in struct
// and output the field value in correct types
func getTransformedValueFromValidField(modelObj interface{}, structFieldName string, urlFieldValues []string) ([]interface{}, error) {
	// Important!! Check if fieldName is actually part of the schema, otherwise risk of sequal injection
	fieldType, err := datatypes.GetModelFieldTypeElmIfValid(modelObj, letters.CamelCaseToPascalCase(structFieldName))
	if err != nil {
		return nil, err
	}

	transURLFieldValues, err := datatypes.TransformFieldValue(fieldType.String(), urlFieldValues)
	if err != nil {
		return nil, err
	}

	return transURLFieldValues, nil
}

// getTransformedValueFromValidField make sure the field does exist in struct
// and output the field value in correct types
// func getTransformedValueFromValidField2(modelObj interface{}, structFieldName string, urlFieldValues []string) ([]interface{}, error) {
// 	// Important!! Check if fieldName is actually part of the schema, otherwise risk of sequal injection
// 	fieldType, err := datatypes.GetModelFieldTypeElmIfValid(modelObj, letters.CamelCaseToPascalCase(structFieldName))
// 	if err != nil {
// 		return nil, err
// 	}

// 	log.Println("fieldType:", fieldType)

// 	transURLFieldValues, err := datatypes.TransformFieldValue(fieldType.String(), urlFieldValues)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return transURLFieldValues, nil
// }

func filterNullValue(transformedFieldValues []interface{}) (filtered []interface{}, anyNull bool) {
	// Filter out the "null" ones
	anyNull = false
	filtered = make([]interface{}, 0)
	for _, value := range transformedFieldValues {
		if isNil(value) {
			anyNull = true
		} else { // when isNil panic and recovered it goes here..I'm not sure how it works but this is what I need
			filtered = append(filtered, value)
		}
	}
	return filtered, anyNull
}

// https://mangatmodi.medium.com/go-check-nil-interface-the-right-way-d142776edef1
func isNil(i interface{}) bool {
	// Will panic for value type such as string and int
	defer func() {
		if r := recover(); r != nil {
			// fmt.Println("Recovered in f", r)
			return // for string type and stuff..
		}
	}()
	return i == nil || reflect.ValueOf(i).IsNil()
}
