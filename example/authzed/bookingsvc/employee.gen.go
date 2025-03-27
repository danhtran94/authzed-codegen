// Code generated by spicedb-gen, DO NOT EDIT.

package bookingsvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  "context"
)

const TypeEmployee authz.Type = "bookingsvc/employee"
type RelationEmployee authz.Relation
type PermissionEmployee authz.Permission

const EmployeeAccount RelationEmployee = "account"
type EmployeeAccountObjects struct {
  User []User
}
const EmployeeBelongsBrand RelationEmployee = "belongs_brand"
type EmployeeBelongsBrandObjects struct {
  Brand []Brand
}
const EmployeeViewer RelationEmployee = "viewer"
type EmployeeViewerObjects struct {
  User []User
}

type Employee authz.ID
func EmployeeStringer(id authz.StringConvertable) Employee {
  return Employee(id.String())
}

func EmployeeStringers(ids ...authz.StringConvertable) []Employee {
  result := []Employee{}
  for _, id := range ids {
    result = append(result, Employee(id.String()))
  }
  return result
}

func (employee Employee) ToList() []Employee {
  return []Employee{ employee }
}

func (employee Employee) CreateAccountRelations(ctx context.Context, objects EmployeeAccountObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Relation(EmployeeAccount), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  return nil
}
func (employee Employee) CreateBelongsBrandRelations(ctx context.Context, objects EmployeeBelongsBrandObjects) error {
  if len(objects.Brand) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Relation(EmployeeBelongsBrand), TypeBrand, authz.IDs(objects.Brand))
    if err != nil {
      return err
    }
  }
  return nil
}
func (employee Employee) CreateViewerRelations(ctx context.Context, objects EmployeeViewerObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Relation(EmployeeViewer), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  return nil
}

func (employee Employee) DeleteAccountRelations(ctx context.Context, objects EmployeeAccountObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Relation(EmployeeAccount), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  return nil
}

func (employee Employee) DeleteBelongsBrandRelations(ctx context.Context, objects EmployeeBelongsBrandObjects) error {
  if len(objects.Brand) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Relation(EmployeeBelongsBrand), TypeBrand, authz.IDs(objects.Brand))
    if err != nil {
      return err
    }
  }
  return nil
}

func (employee Employee) DeleteViewerRelations(ctx context.Context, objects EmployeeViewerObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Relation(EmployeeViewer), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  return nil
}

func (employee Employee) ReadAccountUserRelations(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeEmployee,
    ID: authz.ID(employee),
  }, authz.Relation(EmployeeAccount), TypeUser)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[User](ids), nil
}

func (employee Employee) ReadBelongsBrandBrandRelations(ctx context.Context) ([]Brand, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeEmployee,
    ID: authz.ID(employee),
  }, authz.Relation(EmployeeBelongsBrand), TypeBrand)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Brand](ids), nil
}

func (employee Employee) ReadViewerUserRelations(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeEmployee,
    ID: authz.ID(employee),
  }, authz.Relation(EmployeeViewer), TypeUser)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[User](ids), nil
}

const EmployeeManage PermissionEmployee = "manage"

type CheckEmployeeManageInputs struct {
  User []User
}

func (employee Employee) CheckManage(ctx context.Context, input CheckEmployeeManageInputs) (bool, error) {
  if len(input.User) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Permission(EmployeeManage), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupManageEmployeeResources(ctx context.Context, input CheckEmployeeManageInputs) ([]Employee, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(ctx,
      TypeEmployee, authz.Permission(EmployeeManage), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Employee](ids), nil
  }
  
  return []Employee{}, nil
}
const EmployeeView PermissionEmployee = "view"

type CheckEmployeeViewInputs struct {
  User []User
}

func (employee Employee) CheckView(ctx context.Context, input CheckEmployeeViewInputs) (bool, error) {
  if len(input.User) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(ctx, authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, authz.Permission(EmployeeView), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupViewEmployeeResources(ctx context.Context, input CheckEmployeeViewInputs) ([]Employee, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(ctx,
      TypeEmployee, authz.Permission(EmployeeView), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Employee](ids), nil
  }
  
  return []Employee{}, nil
}

func (employee Employee) LookupManageUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(ctx,
    authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, 
    authz.Permission(EmployeeManage), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}

func (employee Employee) LookupViewUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(ctx,
    authz.Resource{
      Type: TypeEmployee,
      ID: authz.ID(employee),
    }, 
    authz.Permission(EmployeeView), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}
