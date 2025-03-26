// Code generated by spicedb-gen, DO NOT EDIT.

package menusvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  "context"
)

const TypeTable authz.Type = "menusvc/table"
type RelationTable authz.Relation
type PermissionTable authz.Permission

const TableOwner RelationTable = "owner"
type TableOwnerObjects struct {
  Company []Company
}

type Table authz.ID

func (table Table) CreateOwnerRelations(ctx context.Context, objects TableOwnerObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(authz.Resource{
      Type: TypeTable,
      ID: authz.ID(table),
    }, authz.Relation(TableOwner), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (table Table) DeleteOwnerRelations(ctx context.Context, objects TableOwnerObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(authz.Resource{
      Type: TypeTable,
      ID: authz.ID(table),
    }, authz.Relation(TableOwner), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (table Table) ReadOwnerCompanyRelations(ctx context.Context) ([]Company, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(authz.Resource{
    Type: TypeTable,
    ID: authz.ID(table),
  }, authz.Relation(TableOwner), TypeCompany)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Company](ids), nil
}

const TableWrite PermissionTable = "write"

type CheckTableWriteInputs struct {
  User []User
}

func (table Table) CheckWrite(ctx context.Context, input CheckTableWriteInputs) (bool, error) {
  if len(input.User) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(authz.Resource{
      Type: TypeTable,
      ID: authz.ID(table),
    }, authz.Permission(TableWrite), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupWriteTableResources(ctx context.Context, input CheckTableWriteInputs) ([]Table, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(
      TypeTable, authz.Permission(TableWrite), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Table](ids), nil
  }
  
  return []Table{}, nil
}

func (table Table) LookupWriteUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(
    authz.Resource{
      Type: TypeTable,
      ID: authz.ID(table),
    }, 
    authz.Permission(TableWrite), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}
