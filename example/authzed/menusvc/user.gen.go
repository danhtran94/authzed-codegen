// Code generated by spicedb-gen, DO NOT EDIT.

package menusvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  "context"
)

const TypeUser authz.Type = "menusvc/user"
type RelationUser authz.Relation
type PermissionUser authz.Permission

const UserBelongsCompany RelationUser = "belongs_company"
type UserBelongsCompanyObjects struct {
  Company []Company
}

type User authz.ID
func UserStringer(id authz.StringConvertable) User {
  return User(id.String())
}

func UserStringers(ids ...authz.StringConvertable) []User {
  result := []User{}
  for _, id := range ids {
    result = append(result, User(id.String()))
  }
  return result
}

func (user User) ToList() []User {
  return []User{ user }
}

func (user User) CreateBelongsCompanyRelations(ctx context.Context, objects UserBelongsCompanyObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeUser,
      ID: authz.ID(user),
    }, authz.Relation(UserBelongsCompany), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (user User) DeleteBelongsCompanyRelations(ctx context.Context, objects UserBelongsCompanyObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeUser,
      ID: authz.ID(user),
    }, authz.Relation(UserBelongsCompany), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (user User) ReadBelongsCompanyCompanyRelations(ctx context.Context) ([]Company, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeUser,
    ID: authz.ID(user),
  }, authz.Relation(UserBelongsCompany), TypeCompany)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Company](ids), nil
}

const UserManage PermissionUser = "manage"

type CheckUserManageInputs struct {
  User []User
}

func (user User) CheckManage(ctx context.Context, input CheckUserManageInputs) (bool, error) {
  if len(input.User) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(ctx, authz.Resource{
      Type: TypeUser,
      ID: authz.ID(user),
    }, authz.Permission(UserManage), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupManageUserResources(ctx context.Context, input CheckUserManageInputs) ([]User, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(ctx,
      TypeUser, authz.Permission(UserManage), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[User](ids), nil
  }
  
  return []User{}, nil
}

func (user User) LookupManageUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(ctx,
    authz.Resource{
      Type: TypeUser,
      ID: authz.ID(user),
    }, 
    authz.Permission(UserManage), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}
