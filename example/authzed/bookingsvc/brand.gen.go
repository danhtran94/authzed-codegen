// Code generated by spicedb-gen, DO NOT EDIT.

package bookingsvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  "context"
)

const TypeBrand authz.Type = "bookingsvc/brand"
type RelationBrand authz.Relation
type PermissionBrand authz.Permission

const BrandAdmin RelationBrand = "admin"
type BrandAdminObjects struct {
  User []User
}
const BrandManager RelationBrand = "manager"
type BrandManagerObjects struct {
  Employee []Employee
}
const BrandEmployee RelationBrand = "employee"
type BrandEmployeeObjects struct {
  Employee []Employee
}

type Brand authz.ID

func (brand Brand) CreateAdminRelations(ctx context.Context, objects BrandAdminObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Relation(BrandAdmin), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  return nil
}

func (brand Brand) CreateManagerRelations(ctx context.Context, objects BrandManagerObjects) error {
  if len(objects.Employee) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Relation(BrandManager), TypeEmployee, authz.IDs(objects.Employee))
    if err != nil {
      return err
    }
  }
  return nil
}

func (brand Brand) CreateEmployeeRelations(ctx context.Context, objects BrandEmployeeObjects) error {
  if len(objects.Employee) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Relation(BrandEmployee), TypeEmployee, authz.IDs(objects.Employee))
    if err != nil {
      return err
    }
  }
  return nil
}

func (brand Brand) DeleteAdminRelations(ctx context.Context, objects BrandAdminObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Relation(BrandAdmin), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  return nil
}

func (brand Brand) DeleteManagerRelations(ctx context.Context, objects BrandManagerObjects) error {
  if len(objects.Employee) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Relation(BrandManager), TypeEmployee, authz.IDs(objects.Employee))
    if err != nil {
      return err
    }
  }
  return nil
}

func (brand Brand) DeleteEmployeeRelations(ctx context.Context, objects BrandEmployeeObjects) error {
  if len(objects.Employee) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Relation(BrandEmployee), TypeEmployee, authz.IDs(objects.Employee))
    if err != nil {
      return err
    }
  }
  return nil
}

func (brand Brand) ReadAdminUserRelations(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(authz.Resource{
    Type: TypeBrand,
    ID: authz.ID(brand),
  }, authz.Relation(BrandAdmin), TypeUser)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[User](ids), nil
}

func (brand Brand) ReadManagerEmployeeRelations(ctx context.Context) ([]Employee, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(authz.Resource{
    Type: TypeBrand,
    ID: authz.ID(brand),
  }, authz.Relation(BrandManager), TypeEmployee)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Employee](ids), nil
}

func (brand Brand) ReadEmployeeEmployeeRelations(ctx context.Context) ([]Employee, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(authz.Resource{
    Type: TypeBrand,
    ID: authz.ID(brand),
  }, authz.Relation(BrandEmployee), TypeEmployee)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Employee](ids), nil
}

const BrandManage PermissionBrand = "manage"

type CheckBrandManageInputs struct {
  Employee []Employee
  User []User
}

func (brand Brand) CheckManage(ctx context.Context, input CheckBrandManageInputs) (bool, error) {
  if len(input.Employee) == 0 && len(input.User) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.Employee) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Permission(BrandManage), TypeEmployee, authz.IDs(input.Employee))
    if err != nil {
      return false, err
    }
  }
  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Permission(BrandManage), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupManageBrandResources(ctx context.Context, input CheckBrandManageInputs) ([]Brand, error) {
  if len(input.Employee) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(
      TypeBrand, authz.Permission(BrandManage), 
      TypeEmployee, authz.IDs(input.Employee),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Brand](ids), nil
  }
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(
      TypeBrand, authz.Permission(BrandManage), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Brand](ids), nil
  }
  
  return []Brand{}, nil
}
const BrandCreateBooking PermissionBrand = "create_booking"

type CheckBrandCreateBookingInputs struct {
  User []User
  Employee []Employee
}

func (brand Brand) CheckCreateBooking(ctx context.Context, input CheckBrandCreateBookingInputs) (bool, error) {
  if len(input.User) == 0 && len(input.Employee) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Permission(BrandCreateBooking), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  if len(input.Employee) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, authz.Permission(BrandCreateBooking), TypeEmployee, authz.IDs(input.Employee))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupCreateBookingBrandResources(ctx context.Context, input CheckBrandCreateBookingInputs) ([]Brand, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(
      TypeBrand, authz.Permission(BrandCreateBooking), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Brand](ids), nil
  }
  if len(input.Employee) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(
      TypeBrand, authz.Permission(BrandCreateBooking), 
      TypeEmployee, authz.IDs(input.Employee),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Brand](ids), nil
  }
  
  return []Brand{}, nil
}

func (brand Brand) LookupManageEmployeeSubjects(ctx context.Context) ([]Employee, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(
    authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, 
    authz.Permission(BrandManage), TypeEmployee,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[Employee](ids), nil
}
func (brand Brand) LookupManageUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(
    authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, 
    authz.Permission(BrandManage), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}

func (brand Brand) LookupCreateBookingUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(
    authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, 
    authz.Permission(BrandCreateBooking), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}
func (brand Brand) LookupCreateBookingEmployeeSubjects(ctx context.Context) ([]Employee, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(
    authz.Resource{
      Type: TypeBrand,
      ID: authz.ID(brand),
    }, 
    authz.Permission(BrandCreateBooking), TypeEmployee,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[Employee](ids), nil
}
