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
	assert.Equal(t, []menusvc.Company{comp}, authz.IDsOf(owners))
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
	assert.Equal(t, []menusvc.Company{comp}, authz.IDsOf(companies))
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
	assert.Equal(t, []menusvc.User{"t-tu1rels"}, authz.IDsOf(admins))

	managers, err := comp.ReadManagerUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-tu2rels"}, authz.IDsOf(managers))

	employees, err := comp.ReadEmployeeUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-te1rels"}, authz.IDsOf(employees))
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
	assert.Equal(t, []menusvc.User{"t-tu3or"}, authz.IDsOf(creators))

	customers, err := order.ReadCreatorCustomerRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Customer{"t-tvc1or"}, authz.IDsOf(customers))

	companies, err := order.ReadBelongsCompanyCompanyRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Company{comp}, authz.IDsOf(companies))
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
	assert.Equal(t, []menusvc.Company{comp}, authz.IDsOf(owners))

	creators, err := booking.ReadCreatorUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"t-tu3br"}, authz.IDsOf(creators))

	customers, err := booking.ReadCreatorCustomerRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.Customer{"t-tvc1br"}, authz.IDsOf(customers))
}


// AUZ-007 ext — caveat codegen in menusvc namespace.

func TestBooking_HoursCheck_GrantsWhenWithinHours(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("hc-ok").CreateHoursKeeperRelations(ctx, menusvc.BookingHoursKeeperObjects{
		User: []menusvc.User{"u-hc"},
	}))

	ok, err := menusvc.Booking("hc-ok").CheckHoursCheck(ctx, menusvc.CheckBookingHoursCheckInputs{
		User: []menusvc.User{"u-hc"},
		Caveats: menusvc.CheckBookingHoursCheckCaveats{
			WithinHours: &menusvc.WithinHoursArgs{
				OpenHour:    new(9),
				CloseHour:   new(17),
				CurrentHour: new(12),
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok, "12 in [9, 17] → grant")
}

// AUZ-007 ext — multi-allowed-type relation where each allowed type
// has a DIFFERENT caveat. User branch gated by within_hours; Customer
// branch by within_months. Generated Caveats sub-struct holds one
// typed pointer per allowed type; the Create method routes each
// branch to its OWN caveat-name literal independently.

func TestBooking_MultiTemporal_GrantsViaUserHours(t *testing.T) {
	ctx := context.Background()

	// Write only the User branch (with within_hours pre-bound).
	require.NoError(t, menusvc.Booking("mt-u").CreateMultiTemporalRelations(ctx, menusvc.BookingMultiTemporalObjects{
		User: []menusvc.User{"u-mt"},
		Caveats: menusvc.BookingMultiTemporalCaveats{
			User: &menusvc.WithinHoursArgs{
				OpenHour:    new(9),
				CloseHour:   new(17),
				CurrentHour: new(12),
			},
			// Customer left nil — no Customer write happens
		},
	}))

	// Check via User — only WithinHours caveat needs binding.
	ok, err := menusvc.Booking("mt-u").CheckMultiTemporalCheck(ctx, menusvc.CheckBookingMultiTemporalCheckInputs{
		User: []menusvc.User{"u-mt"},
		// Caveats can be empty: WithinHours was pre-bound at write time.
	})
	require.NoError(t, err)
	assert.True(t, ok, "User branch grants via within_hours pre-bound at write")
}

func TestBooking_MultiTemporal_GrantsViaCustomerMonths(t *testing.T) {
	ctx := context.Background()

	// Write only the Customer branch (with within_months pre-bound).
	require.NoError(t, menusvc.Booking("mt-c").CreateMultiTemporalRelations(ctx, menusvc.BookingMultiTemporalObjects{
		Customer: []menusvc.Customer{"c-mt"},
		Caveats: menusvc.BookingMultiTemporalCaveats{
			Customer: &menusvc.WithinMonthsArgs{
				OpenMonth:    new(1),
				CloseMonth:   new(12),
				CurrentMonth: new(6),
			},
		},
	}))

	ok, err := menusvc.Booking("mt-c").CheckMultiTemporalCheck(ctx, menusvc.CheckBookingMultiTemporalCheckInputs{
		Customer: []menusvc.Customer{"c-mt"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "Customer branch grants via within_months pre-bound at write")
}

// Both branches in one Create call — atomic mixed-caveat write.
func TestBooking_MultiTemporal_BothBranchesAtomic(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("mt-both").CreateMultiTemporalRelations(ctx, menusvc.BookingMultiTemporalObjects{
		User:     []menusvc.User{"u-mt-both"},
		Customer: []menusvc.Customer{"c-mt-both"},
		Caveats: menusvc.BookingMultiTemporalCaveats{
			User: &menusvc.WithinHoursArgs{
				OpenHour: new(0), CloseHour: new(23), CurrentHour: new(10),
			},
			Customer: &menusvc.WithinMonthsArgs{
				OpenMonth: new(1), CloseMonth: new(12), CurrentMonth: new(6),
			},
		},
	}))

	// Either subject grants
	okU, err := menusvc.Booking("mt-both").CheckMultiTemporalCheck(ctx, menusvc.CheckBookingMultiTemporalCheckInputs{
		User: []menusvc.User{"u-mt-both"},
	})
	require.NoError(t, err)
	assert.True(t, okU)

	okC, err := menusvc.Booking("mt-both").CheckMultiTemporalCheck(ctx, menusvc.CheckBookingMultiTemporalCheckInputs{
		Customer: []menusvc.Customer{"c-mt-both"},
	})
	require.NoError(t, err)
	assert.True(t, okC)
}

// AUZ-007 ext — Edge case 2 (LEGAL): different allowed types gated by
// the SAME caveat name. The Caveats sub-struct disambiguates by
// allowed-type name; both fields point to the same *WithinHoursArgs
// struct type but carry independent per-tuple write-time pre-context.
//
// Edge case 1 (same allowed type with different caveats) is rejected
// at codegen by flattenAllowedTypes — see adapter_test.go for the
// detection unit tests.
func TestBooking_SharedCav_GrantsViaUserOrCustomer(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("sc-u").CreateSharedCavRelations(ctx, menusvc.BookingSharedCavObjects{
		User: []menusvc.User{"u-sc"},
		Caveats: menusvc.BookingSharedCavCaveats{
			User: &menusvc.WithinHoursArgs{
				OpenHour: new(0), CloseHour: new(23), CurrentHour: new(10),
			},
		},
	}))

	require.NoError(t, menusvc.Booking("sc-c").CreateSharedCavRelations(ctx, menusvc.BookingSharedCavObjects{
		Customer: []menusvc.Customer{"c-sc"},
		Caveats: menusvc.BookingSharedCavCaveats{
			Customer: &menusvc.WithinHoursArgs{
				OpenHour: new(0), CloseHour: new(23), CurrentHour: new(10),
			},
		},
	}))

	okU, err := menusvc.Booking("sc-u").CheckSharedCavCheck(ctx, menusvc.CheckBookingSharedCavCheckInputs{
		User: []menusvc.User{"u-sc"},
	})
	require.NoError(t, err)
	assert.True(t, okU, "User branch grants — within_hours pre-bound at write")

	okC, err := menusvc.Booking("sc-c").CheckSharedCavCheck(ctx, menusvc.CheckBookingSharedCavCheckInputs{
		Customer: []menusvc.Customer{"c-sc"},
	})
	require.NoError(t, err)
	assert.True(t, okC, "Customer branch grants — same caveat, independent pre-context")
}

// AUZ-007 ext — Edge case 1 (LEGAL with disambiguation): same allowed
// type gated by DIFFERENT caveats. The codegen disambiguates field
// names by appending the caveat's PascalCase suffix, letting the
// caller pick per-batch which caveat applies.
//
// Schema:
//   relation dup_typed: menusvc/user with menusvc/within_hours
//                     | menusvc/user with menusvc/within_months

func TestBooking_DupTyped_GrantsViaWithinHoursPath(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("dt-h").CreateDupTypedRelations(ctx, menusvc.BookingDupTypedObjects{
		UserWithinHours: []menusvc.User{"u-dt-h"},
		// UserWithinMonths intentionally empty — different write batch
		Caveats: menusvc.BookingDupTypedCaveats{
			UserWithinHours: &menusvc.WithinHoursArgs{
				OpenHour: new(0), CloseHour: new(23), CurrentHour: new(10),
			},
		},
	}))

	ok, err := menusvc.Booking("dt-h").CheckDupTypedCheck(ctx, menusvc.CheckBookingDupTypedCheckInputs{
		User: []menusvc.User{"u-dt-h"},
		// CheckXInputs.Caveats is keyed by caveat name (not allowed-type)
		// so it stays flat: WithinHours + WithinMonths
	})
	require.NoError(t, err)
	assert.True(t, ok, "User-via-within_hours grants — write-time pre-bound")
}

func TestBooking_DupTyped_GrantsViaWithinMonthsPath(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("dt-m").CreateDupTypedRelations(ctx, menusvc.BookingDupTypedObjects{
		UserWithinMonths: []menusvc.User{"u-dt-m"},
		Caveats: menusvc.BookingDupTypedCaveats{
			UserWithinMonths: &menusvc.WithinMonthsArgs{
				OpenMonth: new(1), CloseMonth: new(12), CurrentMonth: new(6),
			},
		},
	}))

	ok, err := menusvc.Booking("dt-m").CheckDupTypedCheck(ctx, menusvc.CheckBookingDupTypedCheckInputs{
		User: []menusvc.User{"u-dt-m"},
	})
	require.NoError(t, err)
	assert.True(t, ok, "User-via-within_months grants — different caveat, separate Create branch")
}

// Read methods deduplicate by namespace — there's only ONE
// ReadDupTypedUserRelations even though two branches share namespace,
// because Read doesn't care which caveat is attached.
func TestBooking_DupTyped_ReadDeduplicatesByNamespace(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("dt-r").CreateDupTypedRelations(ctx, menusvc.BookingDupTypedObjects{
		UserWithinHours: []menusvc.User{"u-r-h"},
		Caveats: menusvc.BookingDupTypedCaveats{
			UserWithinHours: &menusvc.WithinHoursArgs{
				OpenHour: new(0), CloseHour: new(23), CurrentHour: new(10),
			},
		},
	}))

	// Single Read method covers all User relationships of dup_typed,
	// regardless of which caveat is attached at the wire level.
	users, err := menusvc.Booking("dt-r").ReadDupTypedUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []menusvc.User{"u-r-h"}, authz.IDsOf(users))
}

func TestBooking_HoursCheck_DeniesOutsideHours(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, menusvc.Booking("hc-no").CreateHoursKeeperRelations(ctx, menusvc.BookingHoursKeeperObjects{
		User: []menusvc.User{"u-hc-no"},
	}))

	_, err := menusvc.Booking("hc-no").CheckHoursCheck(ctx, menusvc.CheckBookingHoursCheckInputs{
		User: []menusvc.User{"u-hc-no"},
		Caveats: menusvc.CheckBookingHoursCheckCaveats{
			WithinHours: &menusvc.WithinHoursArgs{
				OpenHour:    new(9),
				CloseHour:   new(17),
				CurrentHour: new(22),
			},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}
