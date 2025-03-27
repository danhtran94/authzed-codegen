// Code generated by spicedb-gen, DO NOT EDIT.

package menusvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  
)

const TypeProduct authz.Type = "menusvc/product"
type RelationProduct authz.Relation
type PermissionProduct authz.Permission


type Product authz.ID
func ProductStringer(id authz.StringConvertable) Product {
  return Product(id.String())
}

func ProductStringers(ids ...authz.StringConvertable) []Product {
  result := []Product{}
  for _, id := range ids {
    result = append(result, Product(id.String()))
  }
  return result
}

