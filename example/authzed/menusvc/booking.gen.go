// Code generated by spicedb-gen, DO NOT EDIT.

package menusvc

import (
  "github.com/danhtran94/authzed-codegen/pkg/authz"

  "context"
)

const TypeBooking authz.Type = "menusvc/booking"
type RelationBooking authz.Relation
type PermissionBooking authz.Permission

const BookingOwner RelationBooking = "owner"
type BookingOwnerObjects struct {
  Company []Company
}
const BookingCreator RelationBooking = "creator"
type BookingCreatorObjects struct {
  User []User
  Customer []Customer
}

type Booking authz.ID
func BookingStringer(id authz.StringConvertable) Booking {
  return Booking(id.String())
}

func BookingStringers(ids ...authz.StringConvertable) []Booking {
  result := []Booking{}
  for _, id := range ids {
    result = append(result, Booking(id.String()))
  }
  return result
}

func (booking Booking) ToList() []Booking {
  return []Booking{ booking }
}

func (booking Booking) CreateOwnerRelations(ctx context.Context, objects BookingOwnerObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Relation(BookingOwner), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}
func (booking Booking) CreateCreatorRelations(ctx context.Context, objects BookingCreatorObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Relation(BookingCreator), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  if len(objects.Customer) > 0 {
    err := authz.GetEngine(ctx).CreateRelations(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Relation(BookingCreator), TypeCustomer, authz.IDs(objects.Customer))
    if err != nil {
      return err
    }
  }
  return nil
}

func (booking Booking) DeleteOwnerRelations(ctx context.Context, objects BookingOwnerObjects) error {
  if len(objects.Company) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Relation(BookingOwner), TypeCompany, authz.IDs(objects.Company))
    if err != nil {
      return err
    }
  }
  return nil
}

func (booking Booking) DeleteCreatorRelations(ctx context.Context, objects BookingCreatorObjects) error {
  if len(objects.User) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Relation(BookingCreator), TypeUser, authz.IDs(objects.User))
    if err != nil {
      return err
    }
  }
  if len(objects.Customer) > 0 {
    err := authz.GetEngine(ctx).DeleteRelations(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Relation(BookingCreator), TypeCustomer, authz.IDs(objects.Customer))
    if err != nil {
      return err
    }
  }
  return nil
}

func (booking Booking) ReadOwnerCompanyRelations(ctx context.Context) ([]Company, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeBooking,
    ID: authz.ID(booking),
  }, authz.Relation(BookingOwner), TypeCompany)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Company](ids), nil
}

func (booking Booking) ReadCreatorUserRelations(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeBooking,
    ID: authz.ID(booking),
  }, authz.Relation(BookingCreator), TypeUser)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[User](ids), nil
}

func (booking Booking) ReadCreatorCustomerRelations(ctx context.Context) ([]Customer, error) {
  ids, err := authz.GetEngine(ctx).ReadRelations(ctx, authz.Resource{
    Type: TypeBooking,
    ID: authz.ID(booking),
  }, authz.Relation(BookingCreator), TypeCustomer)
  if err != nil {
    return nil, err
  }
  
  return authz.FromIDs[Customer](ids), nil
}

const BookingWrite PermissionBooking = "write"

type CheckBookingWriteInputs struct {
  User []User
  Customer []Customer
}

func (booking Booking) CheckWrite(ctx context.Context, input CheckBookingWriteInputs) (bool, error) {
  if len(input.User) == 0 && len(input.Customer) == 0 && true {
    return false, authz.ErrNoInput
  }

  if len(input.User) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Permission(BookingWrite), TypeUser, authz.IDs(input.User))
    if err != nil {
      return false, err
    }
  }
  if len(input.Customer) > 0 {
    err := authz.GetEngine(ctx).CheckPermission(ctx, authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, authz.Permission(BookingWrite), TypeCustomer, authz.IDs(input.Customer))
    if err != nil {
      return false, err
    }
  }
  
  return true, nil
}

func LookupWriteBookingResources(ctx context.Context, input CheckBookingWriteInputs) ([]Booking, error) {
  if len(input.User) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(ctx,
      TypeBooking, authz.Permission(BookingWrite), 
      TypeUser, authz.IDs(input.User),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Booking](ids), nil
  }
  if len(input.Customer) > 0 {
    ids, err := authz.GetEngine(ctx).LookupResources(ctx,
      TypeBooking, authz.Permission(BookingWrite), 
      TypeCustomer, authz.IDs(input.Customer),
    )
    if err != nil {
      return nil, err
    }

    return authz.FromIDs[Booking](ids), nil
  }
  
  return []Booking{}, nil
}

func (booking Booking) LookupWriteUserSubjects(ctx context.Context) ([]User, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(ctx,
    authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, 
    authz.Permission(BookingWrite), TypeUser,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[User](ids), nil
}
func (booking Booking) LookupWriteCustomerSubjects(ctx context.Context) ([]Customer, error) {
  ids, err := authz.GetEngine(ctx).LookupSubjects(ctx,
    authz.Resource{
      Type: TypeBooking,
      ID: authz.ID(booking),
    }, 
    authz.Permission(BookingWrite), TypeCustomer,
  )
  if err != nil {
    return nil, err
  }

  return authz.FromIDs[Customer](ids), nil
}
