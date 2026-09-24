package spec

import "strings"

// fixID builds a well-formed fixed ID whose body is padded to the real ULID
// length. Fixed IDs keep golden output byte-stable across runs; tests must
// never mint via NewID or determinism assertions become meaningless.
//
// Suffixes are digits because Crockford base32 excludes I, L, O and U, which
// rules out most mnemonic spellings. fixID validates its own output so a bad
// suffix fails here rather than as confusing downstream diagnostics.
func fixID(kind Kind, suffix string) ID {
	body := "01K2M6RXQ8CJ" + suffix
	if len(body) > ulidLen {
		panic("fixture suffix too long: " + suffix)
	}
	id := ID(string(kind) + "_" + body + strings.Repeat("0", ulidLen-len(body)))
	if !id.Valid(kind) {
		panic("fixture built an invalid id: " + string(id))
	}
	return id
}

var (
	fixProject  = fixID(KindProject, "1")
	fixCustomer = fixID(KindResource, "1")
	fixInvoice  = fixID(KindResource, "2")

	fixCustomerName   = fixID(KindField, "11")
	fixCustomerEmail  = fixID(KindField, "12")
	fixCustomerActive = fixID(KindField, "13")
	fixCustomerTier   = fixID(KindField, "14")

	fixInvoiceCustomer = fixID(KindField, "21")
	fixInvoiceTotal    = fixID(KindField, "22")
	fixInvoicePaid     = fixID(KindField, "23")
	fixInvoiceDue      = fixID(KindField, "24")

	fixPageDashboard = fixID(KindPage, "31")
	fixPageCustomers = fixID(KindPage, "32")
	fixPageInvoices  = fixID(KindPage, "33")

	fixNavDashboard = fixID(KindNav, "41")
	fixNavCustomers = fixID(KindNav, "42")
	fixNavDocs      = fixID(KindNav, "43")
)

func strptr(value string) *string { return &value }

// validSpec is the DESIGN.md §7 Acme CRM example expressed with the stable
// identity model ADR-001 requires. It exercises every MVP field type.
func validSpec() *ProjectSpec {
	return &ProjectSpec{
		SpecVersion: SpecVersion,
		Project: Project{
			ID:   fixProject,
			Name: "Acme CRM",
			Slug: "acme-crm",
		},
		Database: Database{Driver: DriverPostgres},
		Auth:     Auth{Mode: AuthCookie},
		Resources: []*Resource{
			{
				ID:          fixCustomer,
				Label:       "Customer",
				LabelPlural: "Customers",
				CodeName:    "Customer",
				StorageName: "customers",
				Fields: []*Field{
					{
						ID: fixCustomerName, Label: "Name", Type: TypeString,
						CodeName: "Name", StorageName: "name", Required: true,
					},
					{
						ID: fixCustomerEmail, Label: "Email", Type: TypeString,
						CodeName: "Email", StorageName: "email",
						Required: true, Unique: true,
					},
					{
						ID: fixCustomerActive, Label: "Active", Type: TypeBoolean,
						CodeName: "Active", StorageName: "active",
						Default: strptr("true"),
					},
					{
						ID: fixCustomerTier, Label: "Tier", Type: TypeEnum,
						CodeName: "Tier", StorageName: "tier",
						EnumValues: []EnumValue{
							{Value: "free", Label: "Free"},
							{Value: "pro", Label: "Pro"},
						},
					},
				},
				Behavior: ResourceBehavior{
					CreateEnabled: true, UpdateEnabled: true,
					DeleteEnabled: true, AdminVisible: true,
					ListFields:       []ID{fixCustomerName, fixCustomerEmail},
					SearchableFields: []ID{fixCustomerEmail},
				},
			},
			{
				ID:          fixInvoice,
				Label:       "Invoice",
				LabelPlural: "Invoices",
				CodeName:    "Invoice",
				StorageName: "invoices",
				Fields: []*Field{
					{
						ID: fixInvoiceCustomer, Label: "Customer", Type: TypeBelongsTo,
						CodeName: "Customer", StorageName: "customer_id",
						Required: true, Target: fixCustomer, InverseLabel: "Invoices",
					},
					{
						ID: fixInvoiceTotal, Label: "Total", Type: TypeDecimal,
						CodeName: "Total", StorageName: "total", Required: true,
					},
					{
						ID: fixInvoicePaid, Label: "Paid", Type: TypeBoolean,
						CodeName: "Paid", StorageName: "paid", Default: strptr("false"),
					},
					{
						ID: fixInvoiceDue, Label: "Due date", Type: TypeDate,
						CodeName: "DueDate", StorageName: "due_date",
					},
				},
				Behavior: ResourceBehavior{
					CreateEnabled: true, UpdateEnabled: true, AdminVisible: true,
					ListFields: []ID{fixInvoiceTotal, fixInvoicePaid},
				},
			},
		},
		Pages: []*Page{
			{
				ID: fixPageDashboard, Slug: "dashboard", Label: "Dashboard",
				Type: PageDashboard,
				Dashboard: &DashboardConfig{
					CountCards: []DashboardCard{
						{Label: "Customers", Resource: fixCustomer},
					},
					RecentLists: []DashboardCard{
						{Label: "Recent invoices", Resource: fixInvoice, Limit: 5},
					},
				},
			},
			{
				ID: fixPageCustomers, Slug: "customers", Label: "Customers",
				Type: PageResourceTable, Resource: fixCustomer,
				Table: &TableConfig{
					Title:   "Customers",
					Columns: []ID{fixCustomerName, fixCustomerEmail},
					Search:  true, PageSize: 25,
				},
			},
			{
				ID: fixPageInvoices, Slug: "invoices", Label: "Invoices",
				Type: PageResourceTable, Resource: fixInvoice,
				Table: &TableConfig{
					Columns: []ID{fixInvoiceTotal, fixInvoicePaid},
				},
			},
		},
		Navigation: []*NavItem{
			{ID: fixNavDashboard, Label: "Dashboard", Target: NavPage, Page: fixPageDashboard},
			{ID: fixNavCustomers, Label: "Customers", Target: NavPage, Page: fixPageCustomers},
			{ID: fixNavDocs, Label: "Docs", Target: NavExternal, URL: "https://example.com/docs"},
		},
		Branding: &Branding{
			AppName: "Acme CRM", AccentColor: "#3366ff", Appearance: "system",
		},
	}
}
