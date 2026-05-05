package menusvc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	menusvc "github.com/danhtran94/authzed-codegen/example/authzed/menusvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const schemaPath = "../../schema.zed"

type strID string

func (s strID) String() string { return string(s) }

func TestMain(m *testing.M) {
	ctx := context.Background()

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "read schema (%s): %v\n", schemaPath, err)
		os.Exit(1)
	}

	sb, err := spicedbtest.Start(ctx, string(schema))
	if err != nil {
		if errors.Is(err, spicedbtest.ErrDockerUnavailable) {
			fmt.Println("SKIP: Docker not available — skipping menusvc tests")
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(os.Stderr, "start spicedb sandbox: %v\n", err)
		os.Exit(1)
	}
	authz.SetDefaultEngine(sb.Engine)

	code := m.Run()
	_ = sb.Close(ctx)
	os.Exit(code)
}

// --- Customer / Product boilerplate ---

func TestCustomer_Boilerplate(t *testing.T) {
	c := menusvc.CustomerStringer(strID("c-1"))
	require.Equal(t, menusvc.Customer("c-1"), c)

	cs := menusvc.CustomerStringers(strID("a"), strID("b"))
	assert.Equal(t, []menusvc.Customer{"a", "b"}, cs)

	assert.Equal(t, []menusvc.Customer{"c-l"}, menusvc.Customer("c-l").ToList())
}

func TestProduct_Boilerplate(t *testing.T) {
	p := menusvc.ProductStringer(strID("p-1"))
	require.Equal(t, menusvc.Product("p-1"), p)

	ps := menusvc.ProductStringers(strID("a"), strID("b"))
	assert.Equal(t, []menusvc.Product{"a", "b"}, ps)

	assert.Equal(t, []menusvc.Product{"p-l"}, menusvc.Product("p-l").ToList())
}

// --- Table: owner -> manage on company ---

func TestTable_CheckWrite(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-table-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1"},
	}))

	tbl := menusvc.Table("t-table-1")
	require.NoError(t, tbl.CreateOwnerRelations(ctx, menusvc.TableOwnerObjects{
		Company: []menusvc.Company{comp},
	}))

	ok, err := tbl.CheckWrite(ctx, menusvc.CheckTableWriteInputs{
		User: []menusvc.User{"t-tu1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestTable_CheckWrite_Denied(t *testing.T) {
	ctx := context.Background()

	tbl := menusvc.Table("t-table-nobody")
	_, err := tbl.CheckWrite(ctx, menusvc.CheckTableWriteInputs{
		User: []menusvc.User{"t-nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestTable_ReadOwner(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-table-comp-recv")
	tbl := menusvc.Table("t-table-recv")
	require.NoError(t, tbl.CreateOwnerRelations(ctx, menusvc.TableOwnerObjects{
		Company: []menusvc.Company{comp},
	}))

	owners, err := tbl.ReadOwnerCompanyRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Company{comp}, owners)
}

// --- Pricelist: owner -> manage on company ---

func TestPricelist_CheckWrite(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-pl-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-plu1"},
	}))

	pl := menusvc.Pricelist("t-pl-1")
	require.NoError(t, pl.CreateOwnerRelations(ctx, menusvc.PricelistOwnerObjects{
		Company: []menusvc.Company{comp},
	}))

	ok, err := pl.CheckWrite(ctx, menusvc.CheckPricelistWriteInputs{
		User: []menusvc.User{"t-plu1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestPricelist_CheckWrite_Denied(t *testing.T) {
	ctx := context.Background()

	pl := menusvc.Pricelist("t-pl-nobody")
	_, err := pl.CheckWrite(ctx, menusvc.CheckPricelistWriteInputs{
		User: []menusvc.User{"t-nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Setting: owner -> manage on company ---

func TestSetting_CheckWrite(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-set-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-setu1"},
	}))

	s := menusvc.Setting("t-setting-1")
	require.NoError(t, s.CreateOwnerRelations(ctx, menusvc.SettingOwnerObjects{
		Company: []menusvc.Company{comp},
	}))

	ok, err := s.CheckWrite(ctx, menusvc.CheckSettingWriteInputs{
		User: []menusvc.User{"t-setu1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSetting_CheckWrite_Denied(t *testing.T) {
	ctx := context.Background()

	s := menusvc.Setting("t-setting-nobody")
	_, err := s.CheckWrite(ctx, menusvc.CheckSettingWriteInputs{
		User: []menusvc.User{"t-nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- User: belongs_company -> manage ---

func TestUser_CheckManage(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-user-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1"},
	}))

	u := menusvc.User("t-tu1")
	require.NoError(t, u.CreateBelongsCompanyRelations(ctx, menusvc.UserBelongsCompanyObjects{
		Company: []menusvc.Company{comp},
	}))

	ok, err := u.CheckManage(ctx, menusvc.CheckUserManageInputs{
		User: []menusvc.User{"t-tu1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestUser_CheckManage_Denied(t *testing.T) {
	ctx := context.Background()

	u := menusvc.User("t-other")
	_, err := u.CheckManage(ctx, menusvc.CheckUserManageInputs{
		User: []menusvc.User{"t-other"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestUser_ReadBelongsCompany(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-user-bc-comp")
	u := menusvc.User("t-tu1bc")
	require.NoError(t, u.CreateBelongsCompanyRelations(ctx, menusvc.UserBelongsCompanyObjects{
		Company: []menusvc.Company{comp},
	}))

	companies, err := u.ReadBelongsCompanyCompanyRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Company{comp}, companies)
}

// --- Company: manage / create_booking / create_order ---

func TestCompany_CheckManage(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1m"},
	}))
	require.NoError(t, comp.CreateManagerRelations(ctx, menusvc.CompanyManagerObjects{
		User: []menusvc.User{"t-tu2m"},
	}))
	require.NoError(t, comp.CreateEmployeeRelations(ctx, menusvc.CompanyEmployeeObjects{
		User: []menusvc.User{"t-te1m"},
	}))

	ok, err := comp.CheckManage(ctx, menusvc.CheckCompanyManageInputs{
		User: []menusvc.User{"t-tu1m"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, err := comp.CheckManage(ctx, menusvc.CheckCompanyManageInputs{
		User: []menusvc.User{"t-tu2m"},
	})
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestCompany_CheckManage_Denied(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp-nobody")
	_, err := comp.CheckManage(ctx, menusvc.CheckCompanyManageInputs{
		User: []menusvc.User{"t-nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestCompany_CheckCreateBooking(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp-cb")
	require.NoError(t, comp.CreateEmployeeRelations(ctx, menusvc.CompanyEmployeeObjects{
		User: []menusvc.User{"t-te1cb"},
	}))

	ok, err := comp.CheckCreateBooking(ctx, menusvc.CheckCompanyCreateBookingInputs{
		User: []menusvc.User{"t-te1cb"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCompany_CheckCreateBooking_Denied(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp-cb-nobody")
	_, err := comp.CheckCreateBooking(ctx, menusvc.CheckCompanyCreateBookingInputs{
		User: []menusvc.User{"t-nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestCompany_CheckCreateOrder(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp-co")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1co"},
	}))
	require.NoError(t, comp.CreateEmployeeRelations(ctx, menusvc.CompanyEmployeeObjects{
		User: []menusvc.User{"t-te1co"},
	}))

	ok, err := comp.CheckCreateOrder(ctx, menusvc.CheckCompanyCreateOrderInputs{
		User: []menusvc.User{"t-te1co"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, err := comp.CheckCreateOrder(ctx, menusvc.CheckCompanyCreateOrderInputs{
		User: []menusvc.User{"t-tu1co"},
	})
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestCompany_ReadAllRelations(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp-rels")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1rels"},
	}))
	require.NoError(t, comp.CreateManagerRelations(ctx, menusvc.CompanyManagerObjects{
		User: []menusvc.User{"t-tu2rels"},
	}))
	require.NoError(t, comp.CreateEmployeeRelations(ctx, menusvc.CompanyEmployeeObjects{
		User: []menusvc.User{"t-te1rels"},
	}))

	admins, err := comp.ReadAdminUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-tu1rels"}, admins)

	managers, err := comp.ReadManagerUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-tu2rels"}, managers)

	employees, err := comp.ReadEmployeeUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-te1rels"}, employees)
}

func TestCompany_LookupManageSubjects(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-comp-lu")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1lu"},
	}))
	require.NoError(t, comp.CreateManagerRelations(ctx, menusvc.CompanyManagerObjects{
		User: []menusvc.User{"t-tu2lu"},
	}))

	subjects, err := comp.LookupManageUserSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, subjects, menusvc.User("t-tu1lu"))
	assert.Contains(t, subjects, menusvc.User("t-tu2lu"))
}

// --- Order: union creator + belongs_company -> write ---

func TestOrder_CheckWrite(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-order-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1o"},
	}))

	order := menusvc.Order("t-order-1")
	require.NoError(t, order.CreateCreatorRelations(ctx, menusvc.OrderCreatorObjects{
		User:     []menusvc.User{"t-tu3o"},
		Customer: []menusvc.Customer{"t-tvc1o"},
	}))
	require.NoError(t, order.CreateBelongsCompanyRelations(ctx, menusvc.OrderBelongsCompanyObjects{
		Company: []menusvc.Company{comp},
	}))

	ok, err := order.CheckWrite(ctx, menusvc.CheckOrderWriteInputs{
		User: []menusvc.User{"t-tu3o"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, err := order.CheckWrite(ctx, menusvc.CheckOrderWriteInputs{
		Customer: []menusvc.Customer{"t-tvc1o"},
	})
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestOrder_CheckWrite_Denied(t *testing.T) {
	ctx := context.Background()

	order := menusvc.Order("t-order-nobody")
	_, err := order.CheckWrite(ctx, menusvc.CheckOrderWriteInputs{
		Customer: []menusvc.Customer{"t-other"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestOrder_ReadRelations(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-order-comp-rels")
	order := menusvc.Order("t-order-rels")
	require.NoError(t, order.CreateCreatorRelations(ctx, menusvc.OrderCreatorObjects{
		User:     []menusvc.User{"t-tu3or"},
		Customer: []menusvc.Customer{"t-tvc1or"},
	}))
	require.NoError(t, order.CreateBelongsCompanyRelations(ctx, menusvc.OrderBelongsCompanyObjects{
		Company: []menusvc.Company{comp},
	}))

	creators, err := order.ReadCreatorUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-tu3or"}, creators)

	customers, err := order.ReadCreatorCustomerRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Customer{"t-tvc1or"}, customers)

	companies, err := order.ReadBelongsCompanyCompanyRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Company{comp}, companies)
}

func TestOrder_LookupWriteUserSubjects(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-order-lu-comp")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1luo"},
	}))

	order := menusvc.Order("t-order-lu")
	require.NoError(t, order.CreateCreatorRelations(ctx, menusvc.OrderCreatorObjects{
		User: []menusvc.User{"t-tu3luo"},
	}))
	require.NoError(t, order.CreateBelongsCompanyRelations(ctx, menusvc.OrderBelongsCompanyObjects{
		Company: []menusvc.Company{comp},
	}))

	subjects, err := order.LookupWriteUserSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, subjects, menusvc.User("t-tu3luo"))
}

// --- Booking: owner + creator -> write (cross-package: company.create_booking) ---

func TestBooking_CheckWrite(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-booking-comp1")
	require.NoError(t, comp.CreateAdminRelations(ctx, menusvc.CompanyAdminObjects{
		User: []menusvc.User{"t-tu1bk"},
	}))

	booking := menusvc.Booking("t-booking-1")
	require.NoError(t, booking.CreateOwnerRelations(ctx, menusvc.BookingOwnerObjects{
		Company: []menusvc.Company{comp},
	}))
	require.NoError(t, booking.CreateCreatorRelations(ctx, menusvc.BookingCreatorObjects{
		User:     []menusvc.User{"t-tu3bk"},
		Customer: []menusvc.Customer{"t-tvc1bk"},
	}))

	ok, err := booking.CheckWrite(ctx, menusvc.CheckBookingWriteInputs{
		User: []menusvc.User{"t-tu3bk"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBooking_CheckWrite_Denied(t *testing.T) {
	ctx := context.Background()

	booking := menusvc.Booking("t-booking-nobody")
	_, err := booking.CheckWrite(ctx, menusvc.CheckBookingWriteInputs{
		Customer: []menusvc.Customer{"t-other"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

func TestBooking_ReadRelations(t *testing.T) {
	ctx := context.Background()

	comp := menusvc.Company("t-booking-comp-rels")
	booking := menusvc.Booking("t-booking-rels")
	require.NoError(t, booking.CreateOwnerRelations(ctx, menusvc.BookingOwnerObjects{
		Company: []menusvc.Company{comp},
	}))
	require.NoError(t, booking.CreateCreatorRelations(ctx, menusvc.BookingCreatorObjects{
		User:     []menusvc.User{"t-tu3br"},
		Customer: []menusvc.Customer{"t-tvc1br"},
	}))

	owners, err := booking.ReadOwnerCompanyRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Company{comp}, owners)

	creators, err := booking.ReadCreatorUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-tu3br"}, creators)

	customers, err := booking.ReadCreatorCustomerRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Customer{"t-tvc1br"}, customers)
}
