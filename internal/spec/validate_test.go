package spec

import "testing"

func TestValidSpecPasses(t *testing.T) {
	if diagnostics := Validate(validSpec()); diagnostics != nil {
		t.Fatalf("expected valid spec, got:\n%s", diagnostics.Error())
	}
}

// TestValidateRejects covers each invalid category ADR-001 §36 calls out,
// plus the structural rules the generator depends on.
func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ProjectSpec)
		wantAny Code
	}{
		{
			name: "relationship references missing resource",
			mutate: func(s *ProjectSpec) {
				s.Resources[1].Fields[0].Target = fixID(KindResource, "99")
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "relationship target emptied",
			mutate: func(s *ProjectSpec) {
				s.Resources[1].Fields[0].Target = ""
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "duplicate stable id across resources",
			mutate: func(s *ProjectSpec) {
				s.Resources[1].ID = s.Resources[0].ID
			},
			wantAny: CodeDuplicateID,
		},
		{
			name: "duplicate stable id across fields",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[1].ID = s.Resources[0].Fields[0].ID
			},
			wantAny: CodeDuplicateID,
		},
		{
			name: "malformed stable id",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].ID = "res_nope"
			},
			wantAny: CodeMalformedID,
		},
		{
			name: "unsupported field type",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].Type = "money"
			},
			wantAny: CodeUnknownType,
		},
		{
			name: "invalid storage identifier",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].StorageName = "Name With Spaces"
			},
			wantAny: CodeInvalidStorage,
		},
		{
			name: "storage name collides within resource",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[1].StorageName = s.Resources[0].Fields[0].StorageName
			},
			wantAny: CodeDuplicateStore,
		},
		{
			name: "code name is not an exported Go identifier",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].CodeName = "name"
			},
			wantAny: CodeInvalidCodeName,
		},
		{
			name: "code name is a Go keyword",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].CodeName = "range"
			},
			wantAny: CodeInvalidCodeName,
		},
		{
			name: "code name collides within resource namespace",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[1].CodeName = s.Resources[0].Fields[0].CodeName
			},
			wantAny: CodeDuplicateCode,
		},
		{
			// Invoice already has belongs_to "Customer" (generated key CustomerID);
			// a scalar whose code_name is CustomerID emits the same struct field,
			// which bare code_name uniqueness does not see.
			name: "belongs_to key collides with a scalar's generated field",
			mutate: func(s *ProjectSpec) {
				s.Resources[1].Fields = append(s.Resources[1].Fields, &Field{
					ID: fixID(KindField, "29"), Label: "Legacy id", Type: TypeInteger,
					CodeName: "CustomerID", StorageName: "legacy_customer_id",
				})
			},
			wantAny: CodeDuplicateCode,
		},
		{
			name: "enum without values",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[3].EnumValues = nil
			},
			wantAny: CodeInvalidEnum,
		},
		{
			name: "duplicate enum value",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[3].EnumValues = []EnumValue{
					{Value: "free"}, {Value: "free"},
				}
			},
			wantAny: CodeInvalidEnum,
		},
		{
			name: "enum values on a non-enum field",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].EnumValues = []EnumValue{{Value: "x"}}
			},
			wantAny: CodeInvalidEnum,
		},
		{
			name: "page references missing resource",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Resource = fixID(KindResource, "99")
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "table column belongs to another resource",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Table.Columns = []ID{fixInvoiceTotal}
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "duplicate page slug",
			mutate: func(s *ProjectSpec) {
				s.Pages[2].Slug = s.Pages[1].Slug
			},
			wantAny: CodeDuplicateSlug,
		},
		{
			name: "invalid page slug",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Slug = "Not A Slug"
			},
			wantAny: CodeInvalidSlug,
		},
		{
			name: "dashboard must not bind a resource",
			mutate: func(s *ProjectSpec) {
				s.Pages[0].Resource = fixCustomer
			},
			wantAny: CodeInvalidPage,
		},
		{
			name: "dashboard card references missing resource",
			mutate: func(s *ProjectSpec) {
				s.Pages[0].Dashboard.CountCards[0].Resource = fixID(KindResource, "99")
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "behavior references foreign field",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Behavior.ListFields = []ID{fixInvoiceTotal}
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "searchable field is not text-like",
			mutate: func(s *ProjectSpec) {
				// Active is a boolean; ?search= is a text LIKE.
				s.Resources[0].Behavior.SearchableFields = []ID{fixCustomerActive}
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "filterable field is a decimal",
			mutate: func(s *ProjectSpec) {
				// Total is a decimal; exact-match filter excludes decimal (ranges later).
				s.Resources[1].Behavior.FilterableFields = []ID{fixInvoiceTotal}
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "table enables search without a searchable resource field",
			mutate: func(s *ProjectSpec) {
				// The invoices table (Invoice declares no searchable_fields) turns
				// on the search box — a capability the resource doesn't offer.
				s.Pages[2].Table.Search = true
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "table filters by a field the resource didn't declare filterable",
			mutate: func(s *ProjectSpec) {
				// Name is on Customer but Customer declares no filterable_fields.
				s.Pages[1].Table.Filters = []ID{fixCustomerName}
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "table filters by a foreign field",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Table.Filters = []ID{fixInvoiceTotal}
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "recent list order_by references a foreign field",
			mutate: func(s *ProjectSpec) {
				// The recent invoices card orders by a Customer field.
				s.Pages[0].Dashboard.RecentLists[0].OrderBy = fixCustomerName
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "recent list order_by is not declared sortable",
			mutate: func(s *ProjectSpec) {
				// Due is a date on Invoice, but Invoice declares no sortable_fields.
				s.Pages[0].Dashboard.RecentLists[0].OrderBy = fixInvoiceDue
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "recent list order_by is not a date/datetime",
			mutate: func(s *ProjectSpec) {
				// Paid (boolean) is sortable but not temporal.
				s.Resources[1].Behavior.SortableFields = []ID{fixInvoicePaid}
				s.Pages[0].Dashboard.RecentLists[0].OrderBy = fixInvoicePaid
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "count card must not set order_by",
			mutate: func(s *ProjectSpec) {
				s.Pages[0].Dashboard.CountCards[0].OrderBy = fixCustomerName
			},
			wantAny: CodeInvalidPage,
		},
		{
			name: "aggregatable field is not numeric",
			mutate: func(s *ProjectSpec) {
				// Name is a string; SUM/AVG/MIN/MAX need a numeric column.
				s.Resources[0].Behavior.AggregatableFields = []ID{fixCustomerName}
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "aggregatable field is a belongs_to",
			mutate: func(s *ProjectSpec) {
				// The FK is stored numeric but aggregating foreign keys is meaningless.
				s.Resources[1].Behavior.AggregatableFields = []ID{fixInvoiceCustomer}
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "aggregate card references missing resource",
			mutate: func(s *ProjectSpec) {
				s.Pages[0].Dashboard.AggregateCards = []AggregateCard{
					{Label: "X", Resource: fixID(KindResource, "99"), Field: fixInvoiceTotal, Op: AggregateSum},
				}
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "aggregate card label is required",
			mutate: func(s *ProjectSpec) {
				s.Resources[1].Behavior.AggregatableFields = []ID{fixInvoiceTotal}
				s.Pages[0].Dashboard.AggregateCards = []AggregateCard{
					{Resource: fixInvoice, Field: fixInvoiceTotal, Op: AggregateSum},
				}
			},
			wantAny: CodeMissingLabel,
		},
		{
			name: "aggregate card op is unsupported",
			mutate: func(s *ProjectSpec) {
				s.Resources[1].Behavior.AggregatableFields = []ID{fixInvoiceTotal}
				s.Pages[0].Dashboard.AggregateCards = []AggregateCard{
					{Label: "Median total", Resource: fixInvoice, Field: fixInvoiceTotal, Op: AggregateOp("median")},
				}
			},
			wantAny: CodeInvalidPage,
		},
		{
			name: "aggregate card field is foreign to its resource",
			mutate: func(s *ProjectSpec) {
				// A Customer field on an Invoice aggregate card.
				s.Pages[0].Dashboard.AggregateCards = []AggregateCard{
					{Label: "Bad", Resource: fixInvoice, Field: fixCustomerName, Op: AggregateSum},
				}
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "aggregate card field is not declared aggregatable",
			mutate: func(s *ProjectSpec) {
				// Total is numeric and on Invoice, but Invoice never declared it
				// aggregatable, so the card exposes an undeclared capability.
				s.Pages[0].Dashboard.AggregateCards = []AggregateCard{
					{Label: "Total", Resource: fixInvoice, Field: fixInvoiceTotal, Op: AggregateSum},
				}
			},
			wantAny: CodeInvalidCapability,
		},
		{
			name: "navigation references missing page",
			mutate: func(s *ProjectSpec) {
				s.Navigation[0].Page = fixID(KindPage, "99")
			},
			wantAny: CodeDanglingRef,
		},
		{
			name: "external navigation without http url",
			mutate: func(s *ProjectSpec) {
				s.Navigation[2].URL = "ftp://example.com"
			},
			wantAny: CodeInvalidNav,
		},
		{
			name: "unsupported database driver",
			mutate: func(s *ProjectSpec) {
				s.Database.Driver = "oracle"
			},
			wantAny: CodeInvalidDriver,
		},
		{
			name: "unsupported auth mode",
			mutate: func(s *ProjectSpec) {
				s.Auth.Mode = "saml"
			},
			wantAny: CodeInvalidAuth,
		},
		{
			name: "invalid project slug",
			mutate: func(s *ProjectSpec) {
				s.Project.Slug = "Acme CRM"
			},
			wantAny: CodeInvalidSlug,
		},
		{
			name: "missing resource label",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Label = "   "
			},
			wantAny: CodeMissingLabel,
		},
		{
			name: "unsupported form layout",
			mutate: func(s *ProjectSpec) {
				// Convert the page to a real form page so the only defect is
				// the layout, not a type/config mismatch.
				s.Pages[1].Type = PageResourceForm
				s.Pages[1].Table = nil
				s.Pages[1].Form = &FormConfig{Layout: "absolute"}
			},
			wantAny: CodeInvalidPage,
		},

		{
			// The MVP allows at most one resource_form page per resource, so "the
			// form page" a New/Edit link targets is unambiguous (#52).
			name: "two form pages for one resource",
			mutate: func(s *ProjectSpec) {
				s.Pages = append(s.Pages,
					&Page{ID: MustNewID(KindPage), Slug: "edit-customer", Label: "Edit customer", Type: PageResourceForm, Resource: fixCustomer},
					&Page{ID: MustNewID(KindPage), Slug: "edit-customer-2", Label: "Edit customer again", Type: PageResourceForm, Resource: fixCustomer},
				)
			},
			wantAny: CodeDuplicateForm,
		},
		{
			// Likewise at most one resource_detail page per resource (#53).
			name: "two detail pages for one resource",
			mutate: func(s *ProjectSpec) {
				s.Pages = append(s.Pages,
					&Page{ID: MustNewID(KindPage), Slug: "customer", Label: "Customer", Type: PageResourceDetail, Resource: fixCustomer},
					&Page{ID: MustNewID(KindPage), Slug: "customer-again", Label: "Customer again", Type: PageResourceDetail, Resource: fixCustomer},
				)
			},
			wantAny: CodeDuplicateDetail,
		},
		{
			// §4.5: navigation points to a dashboard or a resource list — a
			// detail/form page routes on an :id and is not a nav destination.
			name: "navigation points to a non-navigable page",
			mutate: func(s *ProjectSpec) {
				detail := MustNewID(KindPage)
				s.Pages = append(s.Pages, &Page{ID: detail, Slug: "cust-detail", Label: "Detail", Type: PageResourceDetail, Resource: fixCustomer})
				s.Navigation = append(s.Navigation, &NavItem{ID: MustNewID(KindNav), Label: "Bad", Target: NavPage, Page: detail})
			},
			wantAny: CodeInvalidNav,
		},
		{
			name: "branding accent color is not a hex color",
			mutate: func(s *ProjectSpec) {
				s.Branding = &Branding{AccentColor: "blue"}
			},
			wantAny: CodeInvalidBranding,
		},
		{
			name: "branding appearance is unsupported",
			mutate: func(s *ProjectSpec) {
				s.Branding = &Branding{Appearance: "sepia"}
			},
			wantAny: CodeInvalidBranding,
		},

		// Page type must agree with the attached configuration block,
		// otherwise the domain graph cannot project a single view per page.
		{
			name: "table page carrying a form block",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Form = &FormConfig{Layout: "single_column"}
			},
			wantAny: CodePageMismatch,
		},
		{
			name: "form page carrying a table block",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Type = PageResourceForm
			},
			wantAny: CodePageMismatch,
		},
		{
			name: "dashboard page carrying a table block",
			mutate: func(s *ProjectSpec) {
				s.Pages[0].Table = &TableConfig{}
			},
			wantAny: CodePageMismatch,
		},
		{
			name: "detail page carrying a table block",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Type = PageResourceDetail
			},
			wantAny: CodePageMismatch,
		},
		{
			name: "resource page carrying a dashboard block",
			mutate: func(s *ProjectSpec) {
				s.Pages[1].Dashboard = &DashboardConfig{}
			},
			wantAny: CodePageMismatch,
		},

		// gorm.Model already occupies these names on every generated model.
		{
			name: "code_name reserved by gorm.Model",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].CodeName = "ID"
			},
			wantAny: CodeReservedName,
		},
		{
			name: "code_name CreatedAt reserved by gorm.Model",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].CodeName = "CreatedAt"
			},
			wantAny: CodeReservedName,
		},
		// #19 completed the table: Model (the embedded gorm.Model) and TableName
		// (the generated method) were accepted by the validator before, then
		// rejected by the generator — a spec that validated but would not build.
		{
			name: "code_name Model reserved by embedded gorm.Model",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].CodeName = "Model"
			},
			wantAny: CodeReservedName,
		},
		{
			name: "code_name TableName reserved by the generated model method",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].CodeName = "TableName"
			},
			wantAny: CodeReservedName,
		},
		{
			name: "storage_name reserved by gorm.Model",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].StorageName = "id"
			},
			wantAny: CodeReservedName,
		},
		{
			name: "storage_name deleted_at reserved by gorm.Model",
			mutate: func(s *ProjectSpec) {
				s.Resources[0].Fields[0].StorageName = "deleted_at"
			},
			wantAny: CodeReservedName,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := validSpec()
			test.mutate(s)

			diagnostics := Validate(s)
			if diagnostics == nil {
				t.Fatalf("expected diagnostics, spec validated clean")
			}
			if !diagnostics.Has(test.wantAny) {
				t.Errorf("missing expected code %q; got %v\n%s",
					test.wantAny, diagnostics.Codes(), diagnostics.Error())
			}
		})
	}
}

// TestValidateFieldDefault checks a declared default against its field type.
// An unparseable default reaches the generated model and the migration as a
// literal, so it is a spec-validity question, not a presentation detail.
func TestValidateFieldDefault(t *testing.T) {
	tests := []struct {
		name       string
		fieldType  FieldType
		enumValues []EnumValue
		value      string
		wantValid  bool
	}{
		{name: "string accepts anything", fieldType: TypeString, value: "banana", wantValid: true},
		{name: "text accepts anything", fieldType: TypeText, value: "", wantValid: true},

		{name: "integer accepts digits", fieldType: TypeInteger, value: "42", wantValid: true},
		{name: "integer accepts negative", fieldType: TypeInteger, value: "-7", wantValid: true},
		{name: "integer rejects decimal", fieldType: TypeInteger, value: "1.5"},
		{name: "integer rejects words", fieldType: TypeInteger, value: "banana"},

		{name: "decimal accepts fraction", fieldType: TypeDecimal, value: "1.5", wantValid: true},
		{name: "decimal accepts integer form", fieldType: TypeDecimal, value: "2", wantValid: true},
		{name: "decimal rejects words", fieldType: TypeDecimal, value: "banana"},

		{name: "boolean accepts true", fieldType: TypeBoolean, value: "true", wantValid: true},
		{name: "boolean accepts false", fieldType: TypeBoolean, value: "false", wantValid: true},
		{name: "boolean rejects words", fieldType: TypeBoolean, value: "banana"},
		{name: "boolean rejects yes", fieldType: TypeBoolean, value: "yes"},

		{name: "datetime accepts RFC3339", fieldType: TypeDatetime, value: "2026-08-28T10:00:00Z", wantValid: true},
		{name: "datetime rejects date only", fieldType: TypeDatetime, value: "2026-08-28"},
		{name: "datetime rejects words", fieldType: TypeDatetime, value: "banana"},

		{name: "date accepts YYYY-MM-DD", fieldType: TypeDate, value: "2026-08-28", wantValid: true},
		{name: "date rejects datetime", fieldType: TypeDate, value: "2026-08-28T10:00:00Z"},
		{name: "date rejects impossible day", fieldType: TypeDate, value: "2026-02-31"},

		{
			name: "enum accepts a declared value", fieldType: TypeEnum, value: "pro",
			enumValues: []EnumValue{{Value: "free"}, {Value: "pro"}}, wantValid: true,
		},
		{
			name: "enum rejects an undeclared value", fieldType: TypeEnum, value: "enterprise",
			enumValues: []EnumValue{{Value: "free"}, {Value: "pro"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := validSpec()
			field := s.Resources[0].Fields[0]
			field.Type = test.fieldType
			field.EnumValues = test.enumValues
			field.Default = strptr(test.value)

			diagnostics := Validate(s)
			gotValid := !diagnostics.Has(CodeInvalidDefault)

			if gotValid != test.wantValid {
				t.Errorf("default %q on %s: valid=%v want %v\n%s",
					test.value, test.fieldType, gotValid, test.wantValid, diagnostics.Error())
			}
		})
	}
}

// TestValidateDefaultGrammarIsNarrowerThanStrconv pins the literals a default
// may hold. strconv.ParseBool accepts "1"/"t"/"TRUE" and strconv.ParseFloat
// accepts "NaN"/"Inf"/hex/exponent; those would reach the generated model and
// the migration verbatim, which is the failure this check exists to prevent.
func TestValidateDefaultGrammarIsNarrowerThanStrconv(t *testing.T) {
	booleans := map[string]bool{
		"true": true, "false": true,
		"1": false, "0": false, "t": false, "T": false,
		"TRUE": false, "True": false, "f": false, "F": false, "yes": false,
	}
	for value, wantValid := range booleans {
		t.Run("boolean "+value, func(t *testing.T) {
			assertDefaultValidity(t, TypeBoolean, nil, value, wantValid)
		})
	}

	decimals := map[string]bool{
		"1.5": true, "2": true, "-3.25": true, "+0.5": true, "0": true, ".5": true,
		"NaN": false, "Inf": false, "+Inf": false, "-Inf": false, "infinity": false,
		"0x1p-2": false, "1e5": false, "1.2.3": false, "": false, "-": false, "banana": false,
	}
	for value, wantValid := range decimals {
		t.Run("decimal "+value, func(t *testing.T) {
			assertDefaultValidity(t, TypeDecimal, nil, value, wantValid)
		})
	}

	integers := map[string]bool{
		"42": true, "-7": true, "+5": true,
		"0x10": false, "1_0": false, "1.5": false, "NaN": false, "": false,
	}
	for value, wantValid := range integers {
		t.Run("integer "+value, func(t *testing.T) {
			assertDefaultValidity(t, TypeInteger, nil, value, wantValid)
		})
	}
}

func assertDefaultValidity(t *testing.T, fieldType FieldType, enums []EnumValue, value string, wantValid bool) {
	t.Helper()

	s := validSpec()
	field := s.Resources[0].Fields[0]
	field.Type = fieldType
	field.EnumValues = enums
	field.Default = strptr(value)

	diagnostics := Validate(s)
	if gotValid := !diagnostics.Has(CodeInvalidDefault); gotValid != wantValid {
		t.Errorf("default %q on %s: valid=%v want %v", value, fieldType, gotValid, wantValid)
	}
}

// TestValidateRejectsUnrepresentableDefault keeps spec validity in step with
// what the generator can emit: a string default the compiler cannot carry into
// a GORM tag is rejected at validation, not accepted and then refused at
// generation.
func TestValidateRejectsUnrepresentableDefault(t *testing.T) {
	unsafe := map[string]string{
		"semicolon": "a;b",
		"quote":     `a"b`,
		"backtick":  "a`b",
		"backslash": `a\b`,
		"newline":   "a\nb",
		"tab":       "a\tb",
		"nul":       "a\x00b",
	}
	for name, value := range unsafe {
		t.Run("string "+name, func(t *testing.T) {
			s := validSpec()
			s.Resources[0].Fields[0].Default = strptr(value) // Email, a string field
			if diagnostics := Validate(s); !diagnostics.Has(CodeInvalidDefault) {
				t.Errorf("default %q should be rejected as unrepresentable, got %v", value, diagnostics.Codes())
			}
		})
	}

	// An enum default that is a declared value but itself unrepresentable is
	// rejected too.
	t.Run("enum value with semicolon", func(t *testing.T) {
		s := validSpec()
		tier := s.Resources[0].Fields[3] // Tier enum
		tier.EnumValues = []EnumValue{{Value: "a;b"}}
		tier.Default = strptr("a;b")
		if diagnostics := Validate(s); !diagnostics.Has(CodeInvalidDefault) {
			t.Errorf("unrepresentable enum default should be rejected, got %v", diagnostics.Codes())
		}
	})

	// Safe values still pass: a plain string and an apostrophe.
	for _, ok := range []string{"free", "O'Brien", "hello world", "50%"} {
		t.Run("safe "+ok, func(t *testing.T) {
			s := validSpec()
			s.Resources[0].Fields[0].Default = strptr(ok)
			if diagnostics := Validate(s); diagnostics.Has(CodeInvalidDefault) {
				t.Errorf("default %q should be accepted, got %s", ok, diagnostics.Error())
			}
		})
	}
}

func TestValidateRejectsDefaultOnBelongsTo(t *testing.T) {
	s := validSpec()
	s.Resources[1].Fields[0].Default = strptr("anything")

	if diagnostics := Validate(s); !diagnostics.Has(CodeInvalidDefault) {
		t.Errorf("expected a belongs_to default to be rejected, got %v", diagnostics.Codes())
	}
}

// TestValidateReportsAllProblems confirms validation accumulates rather than
// stopping at the first failure, so the editor can show everything at once.
func TestValidateReportsAllProblems(t *testing.T) {
	s := validSpec()
	s.Database.Driver = "oracle"
	s.Auth.Mode = "saml"
	s.Resources[0].Fields[0].Type = "money"

	diagnostics := Validate(s)
	if len(diagnostics) < 3 {
		t.Fatalf("expected at least 3 diagnostics, got %d:\n%s",
			len(diagnostics), diagnostics.Error())
	}
	for _, want := range []Code{CodeInvalidDriver, CodeInvalidAuth, CodeUnknownType} {
		if !diagnostics.Has(want) {
			t.Errorf("missing %q in %v", want, diagnostics.Codes())
		}
	}
}

// TestValidateAllowsSameCodeNameInDifferentResources encodes ADR-001 §7: two
// resources may each legitimately expose Email; only same-namespace
// collisions are errors.
func TestValidateAllowsSameCodeNameInDifferentResources(t *testing.T) {
	s := validSpec()
	s.Resources[1].Fields[1].CodeName = "Email"
	s.Resources[1].Fields[1].StorageName = "email"

	if diagnostics := Validate(s); diagnostics != nil {
		t.Fatalf("cross-resource symbol reuse should be legal, got:\n%s", diagnostics.Error())
	}
}

// TestValidateAcceptsSupportedQueryCapabilityTypes pins the accepted side of
// the per-type rules: filterable over string/enum/bool and a belongs_to FK,
// searchable over string/enum, and sortable over any field type (decimal and
// date included) all validate cleanly.
func TestValidateAcceptsSupportedQueryCapabilityTypes(t *testing.T) {
	s := validSpec()
	s.Resources[0].Behavior.FilterableFields = []ID{fixCustomerName, fixCustomerActive, fixCustomerTier}
	s.Resources[0].Behavior.SearchableFields = []ID{fixCustomerName, fixCustomerTier}
	s.Resources[0].Behavior.SortableFields = []ID{fixCustomerName, fixCustomerActive, fixCustomerTier}
	// belongs_to is filterable; every scalar including decimal/date is sortable.
	s.Resources[1].Behavior.FilterableFields = []ID{fixInvoiceCustomer}
	s.Resources[1].Behavior.SortableFields = []ID{fixInvoiceTotal, fixInvoiceDue}

	if diagnostics := Validate(s); diagnostics.Has(CodeInvalidCapability) {
		t.Fatalf("supported capability types should validate, got:\n%s", diagnostics.Error())
	}
}

// TestValidateAcceptsTableFilterSubset: a table may filter by a field the
// resource declares filterable, even one that is not a visible column.
func TestValidateAcceptsTableFilterSubset(t *testing.T) {
	s := validSpec()
	// Declare Name filterable and have the customers table filter by it —
	// Name is not in the table's Columns, which is allowed.
	s.Resources[0].Behavior.FilterableFields = []ID{fixCustomerName, fixCustomerTier}
	s.Pages[1].Table.Filters = []ID{fixCustomerName, fixCustomerTier}
	s.Pages[1].Table.Columns = []ID{fixCustomerEmail}

	if diagnostics := Validate(s); diagnostics.Has(CodeInvalidCapability) || diagnostics.Has(CodeDanglingRef) {
		t.Fatalf("a filter subset of the resource's filterable fields should validate, got:\n%s", diagnostics.Error())
	}
}

// TestValidateAcceptsRecentListOrderBy: a recent list may order by a sortable
// date/datetime field the resource declares (#54).
func TestValidateAcceptsRecentListOrderBy(t *testing.T) {
	s := validSpec()
	// Due is a date on Invoice; declare it sortable and order the recent invoices
	// card by it.
	s.Resources[1].Behavior.SortableFields = []ID{fixInvoiceDue}
	s.Pages[0].Dashboard.RecentLists[0].OrderBy = fixInvoiceDue

	if diagnostics := Validate(s); diagnostics.Has(CodeInvalidCapability) || diagnostics.Has(CodeDanglingRef) {
		t.Fatalf("a sortable date order_by should validate, got:\n%s", diagnostics.Error())
	}
}

// TestValidateAcceptsAggregateCard: a dashboard aggregate card over a numeric
// field the resource declares aggregatable validates (#182).
func TestValidateAcceptsAggregateCard(t *testing.T) {
	s := validSpec()
	// Total is a decimal on Invoice; declare it aggregatable and sum it.
	s.Resources[1].Behavior.AggregatableFields = []ID{fixInvoiceTotal}
	s.Pages[0].Dashboard.AggregateCards = []AggregateCard{
		{Label: "Total invoiced", Resource: fixInvoice, Field: fixInvoiceTotal, Op: AggregateSum},
		{Label: "Largest invoice", Resource: fixInvoice, Field: fixInvoiceTotal, Op: AggregateMax},
	}

	if diagnostics := Validate(s); diagnostics.Has(CodeInvalidCapability) ||
		diagnostics.Has(CodeDanglingRef) || diagnostics.Has(CodeInvalidPage) {
		t.Fatalf("an aggregate card over a declared-aggregatable numeric field should validate, got:\n%s", diagnostics.Error())
	}
}

func TestValidateNilSpec(t *testing.T) {
	if diagnostics := Validate(nil); diagnostics == nil {
		t.Fatal("expected diagnostics for nil spec")
	}
}

// TestValidateSurvivesNullArrayEntries covers JSON that Unmarshal accepts but
// that carries null entries. Validation must report them as diagnostics; a
// lookup that walks the null entry must not crash.
func TestValidateSurvivesNullArrayEntries(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			// A null resource plus a belongs_to whose target lookup scans it.
			name: "null resource with a belongs_to lookup",
			json: `{"spec_version":1,
"project":{"id":"` + string(fixProject) + `","name":"Acme","slug":"acme"},
"database":{"driver":"postgres"},"auth":{"mode":"cookie"},
"resources":[null,{"id":"` + string(fixInvoice) + `","label":"Invoice",
"code_name":"Invoice","storage_name":"invoices","fields":[
{"id":"` + string(fixInvoiceCustomer) + `","label":"Customer","type":"belongs_to",
"code_name":"Customer","storage_name":"customer_id","target":"` + string(fixCustomer) + `"}],
"behavior":{"create_enabled":true,"update_enabled":true,"delete_enabled":true,"admin_visible":true}}],
"pages":[],"navigation":[]}`,
		},
		{
			// A null field plus a behavior list that scans it.
			name: "null field with a behavior lookup",
			json: `{"spec_version":1,
"project":{"id":"` + string(fixProject) + `","name":"Acme","slug":"acme"},
"database":{"driver":"postgres"},"auth":{"mode":"cookie"},
"resources":[{"id":"` + string(fixCustomer) + `","label":"Customer",
"code_name":"Customer","storage_name":"customers","fields":[null],
"behavior":{"create_enabled":true,"update_enabled":true,"delete_enabled":true,
"admin_visible":true,"list_fields":["` + string(fixCustomerName) + `"]}}],
"pages":[],"navigation":[]}`,
		},
		{
			// A null page plus a nav entry that scans it.
			name: "null page with a navigation lookup",
			json: `{"spec_version":1,
"project":{"id":"` + string(fixProject) + `","name":"Acme","slug":"acme"},
"database":{"driver":"postgres"},"auth":{"mode":"cookie"},
"resources":[],"pages":[null],
"navigation":[{"id":"` + string(fixNavDashboard) + `","label":"Dashboard",
"target":"page","page":"` + string(fixPageDashboard) + `"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, err := Unmarshal([]byte(test.json))
			if err != nil {
				t.Fatalf("fixture JSON should decode: %v", err)
			}

			// The point of the test: this must not panic.
			diagnostics := Validate(s)
			if diagnostics == nil {
				t.Fatal("expected diagnostics for a spec containing null entries")
			}

			// Marshalling such a spec must not crash either.
			if _, err := Marshal(s); err != nil {
				t.Errorf("marshal: %v", err)
			}
		})
	}
}

func TestNameHelpers(t *testing.T) {
	identTests := map[string]bool{
		"Email": true, "ContactEmail": true, "Email2": true, "TwoFactorEnabled": true,
		"email": false, "2Factor": false, "": false, "range": false,
		"Contact Email": false, "Contact-Email": false, "_Email": false,
	}
	for input, want := range identTests {
		if got := IsExportedGoIdent(input); got != want {
			t.Errorf("IsExportedGoIdent(%q) = %v, want %v", input, got, want)
		}
	}

	storageTests := map[string]bool{
		"email": true, "contact_email": true, "line_1": true,
		"Email": false, "contact__email": false, "contact_": false,
		"_email": false, "1st": false, "": false, "type": true,
	}
	for input, want := range storageTests {
		if got := IsStorageName(input); got != want {
			t.Errorf("IsStorageName(%q) = %v, want %v", input, got, want)
		}
	}

	slugTests := map[string]bool{
		"customers": true, "acme-crm": true, "v2-invoices": true,
		"Customers": false, "acme--crm": false, "-crm": false, "crm-": false, "": false,
	}
	for input, want := range slugTests {
		if got := IsSlug(input); got != want {
			t.Errorf("IsSlug(%q) = %v, want %v", input, got, want)
		}
	}
}
