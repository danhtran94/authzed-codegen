package bookingsvc_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	bookingsvc "github.com/danhtran94/authzed-codegen/example/authzed/bookingsvc"
	"github.com/danhtran94/authzed-codegen/pkg/authz"
	"github.com/danhtran94/authzed-codegen/pkg/authz/spicedbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const schemaPath = "../../schema.zed"

// strID is a tiny helper implementing authz.StringConvertable so the
// generated *Stringer / *Stringers helpers can be exercised. The codegen
// targets these helpers at upstream ID types (e.g. uuid.UUID) that
// implement String() string, not the codegen's own ~string types.
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
			fmt.Println("SKIP: Docker not available — skipping bookingsvc tests")
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

// --- Customer boilerplate ---

func TestCustomer_Boilerplate(t *testing.T) {
	c := bookingsvc.CustomerStringer(strID("c-1"))
	require.Equal(t, bookingsvc.Customer("c-1"), c)

	cs := bookingsvc.CustomerStringers(strID("a"), strID("b"))
	assert.Equal(t, []bookingsvc.Customer{"a", "b"}, cs)

	list := bookingsvc.Customer("c-list").ToList()
	assert.Equal(t, []bookingsvc.Customer{"c-list"}, list)
}

// --- User boilerplate ---

func TestUser_Boilerplate(t *testing.T) {
	u := bookingsvc.UserStringer(strID("u-1"))
	require.Equal(t, bookingsvc.User("u-1"), u)

	us := bookingsvc.UserStringers(strID("x"), strID("y"))
	assert.Equal(t, []bookingsvc.User{"x", "y"}, us)

	list := bookingsvc.User("u-list").ToList()
	assert.Equal(t, []bookingsvc.User{"u-list"}, list)
}

// --- Brand: CheckManage ---

func TestBrand_CheckManage(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-brand-admin-manage")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u1"},
	}))
	require.NoError(t, brand.CreateManagerRelations(ctx, bookingsvc.BrandManagerObjects{
		Employee: []bookingsvc.Employee{"t-e1"},
	}))

	ok, err := brand.CheckManage(ctx, bookingsvc.CheckBrandManageInputs{
		User: []bookingsvc.User{"t-u1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, err := brand.CheckManage(ctx, bookingsvc.CheckBrandManageInputs{
		Employee: []bookingsvc.Employee{"t-e1"},
	})
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestBrand_CheckManage_Denied(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-brand-no-admin")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u2"},
	}))

	_, err := brand.CheckManage(ctx, bookingsvc.CheckBrandManageInputs{
		User: []bookingsvc.User{"nobody"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Brand: CheckCreateBooking ---

func TestBrand_CheckCreateBooking(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-brand-employee-create")
	require.NoError(t, brand.CreateEmployeeRelations(ctx, bookingsvc.BrandEmployeeObjects{
		Employee: []bookingsvc.Employee{"t-e2"},
	}))

	ok, err := brand.CheckCreateBooking(ctx, bookingsvc.CheckBrandCreateBookingInputs{
		Employee: []bookingsvc.Employee{"t-e2"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBrand_CheckCreateBooking_Denied(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-brand-no-employee")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u3"},
	}))

	_, err := brand.CheckCreateBooking(ctx, bookingsvc.CheckBrandCreateBookingInputs{
		Employee: []bookingsvc.Employee{"t-e3"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Brand: ReadRelations ---

func TestBrand_ReadRelations(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-brand-read-rels")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u4"},
	}))
	require.NoError(t, brand.CreateManagerRelations(ctx, bookingsvc.BrandManagerObjects{
		Employee: []bookingsvc.Employee{"t-e4"},
	}))

	admins, err := brand.ReadAdminUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []bookingsvc.User{"t-u4"}, authz.IDsOf(admins))

	managers, err := brand.ReadManagerEmployeeRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []bookingsvc.Employee{"t-e4"}, authz.IDsOf(managers))
}

// --- Brand: LookupSubjects (manage) ---

func TestBrand_LookupManageSubjects(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-brand-lookup-subjects")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u5"},
	}))
	require.NoError(t, brand.CreateManagerRelations(ctx, bookingsvc.BrandManagerObjects{
		Employee: []bookingsvc.Employee{"t-e5"},
	}))

	users, err := brand.LookupManageUserSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, users, bookingsvc.User("t-u5"))

	employees, err := brand.LookupManageEmployeeSubjects(ctx)
	require.NoError(t, err)
	assert.Contains(t, employees, bookingsvc.Employee("t-e5"))
}

// --- Employee: CheckManage (self-ref + cross-def arrow) ---

func TestEmployee_CheckManage(t *testing.T) {
	ctx := context.Background()

	em := bookingsvc.Employee("t-em-manage")

	brand := bookingsvc.Brand("t-em-brand-manage")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u6"},
	}))
	// Make the employee a manager of the brand so brand.manage resolves
	// to the employee subject (the self-ref leg below).
	require.NoError(t, brand.CreateManagerRelations(ctx, bookingsvc.BrandManagerObjects{
		Employee: []bookingsvc.Employee{em},
	}))

	require.NoError(t, em.CreateAccountRelations(ctx, bookingsvc.EmployeeAccountObjects{
		User: []bookingsvc.User{"t-u6"},
	}))
	require.NoError(t, em.CreateBelongsBrandRelations(ctx, bookingsvc.EmployeeBelongsBrandObjects{
		Brand: []bookingsvc.Brand{brand},
	}))

	// Direct: account=user
	ok, err := em.CheckManage(ctx, bookingsvc.CheckEmployeeManageInputs{
		User: []bookingsvc.User{"t-u6"},
	})
	require.NoError(t, err)
	assert.True(t, ok)

	// Self-ref: belongs_brand->manage — employee is brand manager, so
	// brand.manage resolves back to the employee subject.
	ok2, err := em.CheckManage(ctx, bookingsvc.CheckEmployeeManageInputs{
		Employee: []bookingsvc.Employee{em},
	})
	require.NoError(t, err)
	assert.True(t, ok2)
}

func TestEmployee_CheckManage_Denied(t *testing.T) {
	ctx := context.Background()

	em := bookingsvc.Employee("t-em-manage-deny")
	require.NoError(t, em.CreateAccountRelations(ctx, bookingsvc.EmployeeAccountObjects{
		User: []bookingsvc.User{"t-u6b"},
	}))

	_, err := em.CheckManage(ctx, bookingsvc.CheckEmployeeManageInputs{
		User: []bookingsvc.User{"notaccount"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Employee: CheckView (wildcard + direct) ---

func TestEmployee_CheckView_Wildcard(t *testing.T) {
	ctx := context.Background()

	em := bookingsvc.Employee("t-em-view-wildcard")
	require.NoError(t, em.CreateViewerRelations(ctx, bookingsvc.EmployeeViewerObjects{
		Wildcards: bookingsvc.EmployeeViewerWildcards{User: true},
	}))

	ok, err := em.CheckView(ctx, bookingsvc.CheckEmployeeViewInputs{
		User: []bookingsvc.User{"anyone"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

// view = manage + viewer; viewer is wildcard-only on the schema, so the
// direct-user branch flows through manage (account=user grants manage,
// which feeds view).
func TestEmployee_CheckView_ViaManage(t *testing.T) {
	ctx := context.Background()

	em := bookingsvc.Employee("t-em-view-via-manage")
	require.NoError(t, em.CreateAccountRelations(ctx, bookingsvc.EmployeeAccountObjects{
		User: []bookingsvc.User{"t-u7"},
	}))

	ok, err := em.CheckView(ctx, bookingsvc.CheckEmployeeViewInputs{
		User: []bookingsvc.User{"t-u7"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

// --- Employee: ReadViewerUserWildcard ---

func TestEmployee_ReadViewerUserWildcard(t *testing.T) {
	ctx := context.Background()

	em := bookingsvc.Employee("t-em-viewer-wc")
	require.NoError(t, em.CreateViewerRelations(ctx, bookingsvc.EmployeeViewerObjects{
		Wildcards: bookingsvc.EmployeeViewerWildcards{User: true},
	}))

	_, isWildcard, err := em.ReadViewerUserWildcard(ctx)
	require.NoError(t, err)
	assert.True(t, isWildcard)
}

// --- Employee: ReadAccountUserRelations ---

func TestEmployee_ReadAccountUserRelations(t *testing.T) {
	ctx := context.Background()

	em := bookingsvc.Employee("t-em-read-account")
	require.NoError(t, em.CreateAccountRelations(ctx, bookingsvc.EmployeeAccountObjects{
		User: []bookingsvc.User{"t-u-read"},
	}))

	users, err := em.ReadAccountUserRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []bookingsvc.User{"t-u-read"}, authz.IDsOf(users))
}

// --- Booking: CheckWrite (cross-def arrows) ---

func TestBooking_CheckWrite_OwnerArrow(t *testing.T) {
	ctx := context.Background()

	brand := bookingsvc.Brand("t-b-brand-write")
	require.NoError(t, brand.CreateAdminRelations(ctx, bookingsvc.BrandAdminObjects{
		User: []bookingsvc.User{"t-u8"},
	}))

	em := bookingsvc.Employee("t-b-owner-em")
	require.NoError(t, em.CreateBelongsBrandRelations(ctx, bookingsvc.EmployeeBelongsBrandObjects{
		Brand: []bookingsvc.Brand{brand},
	}))

	b := bookingsvc.Booking("t-b-write-owner")
	require.NoError(t, b.CreateOwnerRelations(ctx, bookingsvc.BookingOwnerObjects{
		Employee: []bookingsvc.Employee{em},
	}))

	// owner->manage: user t-u8 is brand admin → has manage on brand → has manage on employee → has write on booking
	ok, err := b.CheckWrite(ctx, bookingsvc.CheckBookingWriteInputs{
		User: []bookingsvc.User{"t-u8"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBooking_CheckWrite_CreatorDirect(t *testing.T) {
	ctx := context.Background()

	b := bookingsvc.Booking("t-b-write-creator")
	require.NoError(t, b.CreateCreatorRelations(ctx, bookingsvc.BookingCreatorObjects{
		Customer: []bookingsvc.Customer{"t-c1"},
	}))

	ok, err := b.CheckWrite(ctx, bookingsvc.CheckBookingWriteInputs{
		Customer: []bookingsvc.Customer{"t-c1"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBooking_CheckWrite_Denied(t *testing.T) {
	ctx := context.Background()

	b := bookingsvc.Booking("t-b-write-deny")

	_, err := b.CheckWrite(ctx, bookingsvc.CheckBookingWriteInputs{
		Customer: []bookingsvc.Customer{"other"},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}

// --- Booking: CheckChangeOwner (creator + creator->manage) ---

func TestBooking_CheckChangeOwner(t *testing.T) {
	ctx := context.Background()

	b := bookingsvc.Booking("t-b-co")
	require.NoError(t, b.CreateCreatorRelations(ctx, bookingsvc.BookingCreatorObjects{
		Customer: []bookingsvc.Customer{"t-c2"},
	}))

	ok, err := b.CheckChangeOwner(ctx, bookingsvc.CheckBookingChangeOwnerInputs{
		Customer: []bookingsvc.Customer{"t-c2"},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

// --- Booking: ReadRelations ---

func TestBooking_ReadRelations(t *testing.T) {
	ctx := context.Background()

	b := bookingsvc.Booking("t-b-read-rels")
	require.NoError(t, b.CreateOwnerRelations(ctx, bookingsvc.BookingOwnerObjects{
		Employee: []bookingsvc.Employee{"t-e-read"},
	}))
	require.NoError(t, b.CreateCreatorRelations(ctx, bookingsvc.BookingCreatorObjects{
		Customer: []bookingsvc.Customer{"t-c-read"},
	}))

	owners, err := b.ReadOwnerEmployeeRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []bookingsvc.Employee{"t-e-read"}, authz.IDsOf(owners))

	creators, err := b.ReadCreatorCustomerRelations(ctx)
	require.NoError(t, err)
	assert.Equal(t, []bookingsvc.Customer{"t-c-read"}, authz.IDsOf(creators))
}


// AUZ-007 ext — caveat codegen in bookingsvc namespace.

func TestBooking_RegionalWrite_GrantsWhenRegionMatches(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, bookingsvc.Booking("rw-ok").CreateRegionalOwnerRelations(ctx, bookingsvc.BookingRegionalOwnerObjects{
		Employee: []bookingsvc.Employee{"e-rw"},
	}))

	ok, err := bookingsvc.Booking("rw-ok").CheckRegionalWrite(ctx, bookingsvc.CheckBookingRegionalWriteInputs{
		Employee: []bookingsvc.Employee{"e-rw"},
		Caveats: bookingsvc.CheckBookingRegionalWriteCaveats{
			RegionMatch: &bookingsvc.RegionMatchArgs{Region: new("asia")},
		},
	})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBooking_RegionalWrite_DeniesWhenRegionMismatches(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, bookingsvc.Booking("rw-no").CreateRegionalOwnerRelations(ctx, bookingsvc.BookingRegionalOwnerObjects{
		Employee: []bookingsvc.Employee{"e-rw-no"},
	}))

	_, err := bookingsvc.Booking("rw-no").CheckRegionalWrite(ctx, bookingsvc.CheckBookingRegionalWriteInputs{
		Employee: []bookingsvc.Employee{"e-rw-no"},
		Caveats: bookingsvc.CheckBookingRegionalWriteCaveats{
			RegionMatch: &bookingsvc.RegionMatchArgs{Region: new("europe")},
		},
	})
	assert.ErrorIs(t, err, authz.ErrPermissionDenied)
}
