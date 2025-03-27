// Code generated by spicedb-gen, DO NOT EDIT.

package menusvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  
)

const TypeCustomer authz.Type = "menusvc/customer"
type RelationCustomer authz.Relation
type PermissionCustomer authz.Permission


type Customer authz.ID
func CustomerStringer(id authz.StringConvertable) Customer {
  return Customer(id.String())
}

func CustomerStringers(ids []authz.StringConvertable) []Customer {
  result := []Customer{}
  for _, id := range ids {
    result = append(result, Customer(id.String()))
  }
  return result
}

