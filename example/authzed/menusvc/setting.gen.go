// Code generated by spicedb-gen, DO NOT EDIT.

package menusvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  "context"
)

const TypeSetting authz.Type = "menusvc/setting"
type RelationSetting authz.Relation
type PermissionSetting authz.Permission

const SettingOwner RelationSetting = "owner"
type SettingOwnerObjects struct {
  Company []Company
}

type Setting authz.ID

func (setting Setting) CreateOwnerRelations(ctx context.Context, objects SettingOwnerObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(authz.Resource{
      Type: TypeSetting,
      ID: authz.ID(setting),
    }, authz.Relation(SettingOwner), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (setting Setting) DeleteOwnerRelations(ctx context.Context, objects SettingOwnerObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(authz.Resource{
      Type: TypeSetting,
      ID: authz.ID(setting),
    }, authz.Relation(SettingOwner), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (setting Setting) ReadOwnerCompanyRelations(ctx context.Context) ([]Company, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(authz.Resource{
    Type: TypeSetting,
    ID: authz.ID(setting),
  }, authz.Relation(SettingOwner), TypeCompany)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Company](ids), nil
}

const SettingWrite PermissionSetting = "write"

type CheckSettingWriteInputs struct {
  User []User
}

func (setting Setting) CheckWrite(ctx context.Context, input CheckSettingWriteInputs) (bool, error) {
  if len(input.User) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(authz.Resource{
      Type: TypeSetting,
      ID: authz.ID(setting),
    }, authz.Permission(SettingWrite), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupWriteSettingResources(ctx context.Context, input CheckSettingWriteInputs) ([]Setting, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(
      TypeSetting, authz.Permission(SettingWrite), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Setting](ids), nil
  }
  
  return []Setting{}, nil
}

func (setting Setting) LookupWriteUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(
    authz.Resource{
      Type: TypeSetting,
      ID: authz.ID(setting),
    }, 
    authz.Permission(SettingWrite), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}
