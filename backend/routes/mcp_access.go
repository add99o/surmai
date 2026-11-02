package routes

import (
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/inflector"
	"github.com/pocketbase/pocketbase/tools/search"
	"github.com/pocketbase/pocketbase/tools/security"
)

// mcpCanCreate mirrors PocketBase's create-rule CTE evaluation without saving
// a record. The caller has already populated the prospective record.
func mcpCanCreate(app core.App, user, record *core.Record) bool {
	if user == nil || record == nil || user.Collection().Name != "users" {
		return false
	}
	collection := record.Collection()
	if collection.CreateRule == nil || collection.IsView() {
		return false
	}
	if *collection.CreateRule == "" {
		return true
	}
	dummy := record.Clone()
	suffix := "__pb_create__" + security.PseudorandomString(12)
	if dummy.Id == "" {
		dummy.Id = "__temp_id__" + suffix
	}
	dummy.SetVerified(false)
	exported, err := dummy.DBExport(app)
	if err != nil {
		return false
	}
	params := make(dbx.Params, len(exported))
	selects := make([]string, 0, len(exported))
	for key, value := range exported {
		key = inflector.Columnify(key)
		param := "__pb_create__" + key
		params[param] = value
		selects = append(selects, "{:"+param+"} AS [["+key+"]]")
	}
	dummyCollection := *collection
	dummyCollection.Id += suffix
	dummyCollection.Name += inflector.Columnify(suffix)
	with := fmt.Sprintf("WITH {{%s}} as (SELECT %s)", dummyCollection.Name, strings.Join(selects, ","))
	query := app.DB().Select("(1)").PreFragment(with).From(dummyCollection.Name).AndBind(params)
	info := &core.RequestInfo{Auth: user, Method: "POST", Body: record.PublicExport()}
	resolver := core.NewRecordFieldResolver(app, &dummyCollection, info, true)
	expr, err := search.FilterData(*dummyCollection.CreateRule).BuildExpr(resolver)
	if err != nil {
		return false
	}
	query.AndWhere(expr)
	if err := resolver.UpdateQuery(query); err != nil {
		return false
	}
	var exists int
	return query.Limit(1).Row(&exists) == nil && exists != 0
}
