package variables

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/hashicorp/waypoint/internal/appconfig"
	"github.com/hashicorp/waypoint/internal/plugin"
	pb "github.com/hashicorp/waypoint/pkg/server/gen"
)

func TestVariables_DecodeVariableBlock(t *testing.T) {
	cases := []struct {
		file string
		err  string
	}{
		{
			"valid.hcl",
			"",
		},
		{
			"invalid_name.hcl",
			"Invalid variable name",
		},
		{
			"invalid_def.hcl",
			"Invalid default value",
		},
		{
			"invalid_type_dynamic.hcl",
			"must be string",
		},
	}

	for _, tt := range cases {
		t.Run(tt.file, func(t *testing.T) {
			require := require.New(t)

			file := filepath.Join("testdata", tt.file)
			base := testConfig{}

			err := hclsimple.DecodeFile(file, nil, &base)
			require.NoError(err)

			schema, _ := gohcl.ImpliedBodySchema(&testConfig{})
			content, diags := base.Body.Content(schema)
			require.False(diags.HasErrors())

			vs := map[string]*Variable{}
			for _, block := range content.Blocks {
				switch block.Type {
				case "variable":
					v, decodeDiag := decodeVariableBlock(nil, block)
					vs[block.Labels[0]] = v
					if decodeDiag.HasErrors() {
						diags = append(diags, decodeDiag...)
					}
				}
			}

			if tt.err == "" {
				require.False(diags.HasErrors(), diags.Error())
				return
			}

			require.True(diags.HasErrors())
			require.Contains(diags.Error(), tt.err)
		})
	}
}

func TestVariables_parseFileValues(t *testing.T) {
	cases := []struct {
		file     string
		expected []*pb.Variable
		err      string
	}{
		{
			file: "values.wpvars",
			expected: []*pb.Variable{
				{
					Name:   "art",
					Value:  &pb.Variable_Str{Str: "gdbee"},
					Source: &pb.Variable_File_{},
				},
				{
					Name:   "mug",
					Value:  &pb.Variable_Str{Str: "yeti"},
					Source: &pb.Variable_File_{},
				},
			},
			err: "",
		},
		{
			file: "complex.wpvars",
			expected: []*pb.Variable{
				{
					Name:   "testlist",
					Value:  &pb.Variable_Hcl{Hcl: "[\"waffles\", \"more waffles\"]"},
					Source: &pb.Variable_File_{},
				},
			},
			err: "",
		},
		{
			file: "nofile.wpvars",
			err:  "Given variables file testdata/nofile.wpvars does not exist",
		},
		{
			file: "nothcl",
			err:  "Missing newline after argument",
		},
		{
			file: "valid.hcl",
			err:  "Variable declaration in a wpvars file",
		},
	}
	for _, tt := range cases {
		t.Run(tt.file, func(t *testing.T) {
			require := require.New(t)

			fp := filepath.Join("testdata", tt.file)
			fv, diags := parseFileValues(fp, "file")

			if tt.err != "" {
				require.Contains(diags.Error(), tt.err)
				return
			}

			require.False(diags.HasErrors())
			require.Equal(len(tt.expected), len(fv))
			for _, v := range tt.expected {
				require.Contains(fv, v)
			}
		})
	}
}

func TestVariables_LoadVCSFile(t *testing.T) {
	cases := []struct {
		name     string
		expected []*pb.Variable
		err      string
	}{
		{
			name: "loads auto file only",
			expected: []*pb.Variable{
				{
					Name:   "mug",
					Value:  &pb.Variable_Str{Str: "ceramic"},
					Source: &pb.Variable_Vcs{},
				},
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			vars, diags := LoadAutoFiles("testdata")

			if tt.err != "" {
				require.True(diags.HasErrors())
				require.Contains(diags.Error(), tt.err)
				return
			}

			require.False(diags.HasErrors())
			require.ElementsMatch(vars, tt.expected)
		})
	}
}

func TestVariables_LoadDynamicDefaults(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		provided []*pb.Variable
		needs    bool
		expected map[string]string
		err      string
	}{
		{
			"no dynamic",
			"no_dynamic.hcl",
			nil,
			false,
			nil,
			"",
		},

		{
			"dynamic but provided",
			"dynamic.hcl",
			[]*pb.Variable{
				{
					Name: "teeth",
					Value: &pb.Variable_Str{
						Str: "pointy",
					},
				},
			},
			false,
			nil,
			"",
		},

		{
			"dynamic need value",
			"dynamic.hcl",
			[]*pb.Variable{
				{
					Name: "irrelevent",
					Value: &pb.Variable_Str{
						Str: "NO",
					},
				},
			},
			true,
			map[string]string{
				"teeth": "hello",
			},
			"",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			file := filepath.Join("testdata", tt.file)
			base := testConfig{}

			err := hclsimple.DecodeFile(file, nil, &base)
			require.NoError(err)

			schema, _ := gohcl.ImpliedBodySchema(&testConfig{})
			content, diags := base.Body.Content(schema)
			require.False(diags.HasErrors())

			vars, diags := DecodeVariableBlocks(nil, content)
			require.False(diags.HasErrors(), diags.Error())

			needs := NeedsDynamicDefaults(tt.provided, vars)
			require.Equal(tt.needs, needs)

			dynVars, diags := LoadDynamicDefaults(
				context.Background(),
				hclog.L(),
				tt.provided,
				vars,
				appconfig.WithPlugins(map[string]*plugin.Instance{
					"static": {
						Component: &appconfig.StaticConfigSourcer{},
					},
				}),
			)
			require.False(diags.HasErrors())

			actual := map[string]string{}
			for _, v := range dynVars {
				actual[v.Name] = v.Value.(*pb.Variable_Str).Str
			}
			if len(actual) == 0 {
				actual = nil
			}
			require.Equal(tt.expected, actual)
		})
	}
}

func TestVariables_EvalInputValues(t *testing.T) {
	cases := []struct {
		name        string
		file        string
		inputValues []*pb.Variable
		expected    Values
		err         string
	}{
		{
			name: "valid",
			file: "valid.hcl",
			inputValues: []*pb.Variable{
				{
					Name:   "art",
					Value:  &pb.Variable_Str{Str: "gdbee"},
					Source: &pb.Variable_Cli{},
				},
				{
					Name:   "dynamic",
					Value:  &pb.Variable_Str{Str: "value"},
					Source: &pb.Variable_Cli{},
				},
			},
			expected: Values{
				"art": &Value{
					cty.StringVal("gdbee"), "cli", hcl.Expression(nil), hcl.Range{},
				},
				"dynamic": &Value{
					cty.StringVal("value"), "cli", hcl.Expression(nil), hcl.Range{},
				},
				"is_good": &Value{
					cty.BoolVal(false), "default", hcl.Expression(nil), hcl.Range{},
				},
				"whatdoesittaketobenumber": &Value{
					cty.NumberIntVal(1), "default", hcl.Expression(nil), hcl.Range{},
				},
			},
			err: "",
		},
		{
			name:        "complex types from default",
			file:        "list.hcl",
			inputValues: []*pb.Variable{},
			expected: Values{
				"testdata": &Value{
					stringListVal("pancakes"), "default", hcl.Expression(nil), hcl.Range{},
				},
			},
			err: "",
		},
		{
			name: "complex types from server",
			file: "list.hcl",
			inputValues: []*pb.Variable{
				{
					Name:   "testdata",
					Value:  &pb.Variable_Hcl{Hcl: "[\"waffles\"]"},
					Source: &pb.Variable_Server{},
				},
			},
			expected: Values{
				"testdata": &Value{
					stringListVal("waffles"), "server", hcl.Expression(nil), hcl.Range{},
				},
			},
			err: "",
		},
		{
			name: "complex types from cli",
			file: "list.hcl",
			inputValues: []*pb.Variable{
				{
					Name:   "testdata",
					Value:  &pb.Variable_Str{Str: "[\"waffles\"]"},
					Source: &pb.Variable_Cli{},
				},
			},
			expected: Values{
				"testdata": &Value{
					stringListVal("waffles"), "cli", hcl.Expression(nil), hcl.Range{},
				},
			},
			err: "",
		},
		{
			name: "undefined variable for pb.Variable value",
			file: "valid.hcl",
			inputValues: []*pb.Variable{
				{
					Name:   "foo",
					Value:  &pb.Variable_Str{Str: "bar"},
					Source: &pb.Variable_Cli{},
				},
			},
			expected: Values{},
			err:      "Undefined variable",
		},
		{
			name: "invalid value type",
			file: "valid.hcl",
			inputValues: []*pb.Variable{
				{
					Name:   "is_good",
					Value:  &pb.Variable_Str{Str: "bar"},
					Source: &pb.Variable_Cli{},
				},
			},
			expected: Values{},
			err:      "Invalid value for variable",
		},
		{
			name: "undefined var for file value",
			file: "undefined.hcl",
			inputValues: []*pb.Variable{
				{
					Name:   "is_good",
					Value:  &pb.Variable_Bool{Bool: true},
					Source: &pb.Variable_Cli{},
				},
			},
			expected: Values{},
			err:      "Undefined variable",
		},
		{
			name:        "no assigned or default value",
			file:        "no_default.hcl",
			inputValues: []*pb.Variable{},
			expected:    Values{},
			err:         "Unset variable",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			file := filepath.Join("testdata", tt.file)
			base := testConfig{}

			err := hclsimple.DecodeFile(file, nil, &base)
			require.NoError(err)

			schema, _ := gohcl.ImpliedBodySchema(&testConfig{})
			content, diags := base.Body.Content(schema)
			require.False(diags.HasErrors())

			vs := map[string]*Variable{}
			for _, block := range content.Blocks {
				switch block.Type {
				case "variable":
					v, decodeDiag := decodeVariableBlock(nil, block)
					vs[block.Labels[0]] = v
					if decodeDiag.HasErrors() {
						diags = append(diags, decodeDiag...)
					}
				}
			}
			require.False(diags.HasErrors())

			ivs, diags := EvaluateVariables(
				hclog.New(&hclog.LoggerOptions{}),
				tt.inputValues,
				vs,
			)
			if tt.err != "" {
				require.True(diags.HasErrors())
				require.Contains(diags.Error(), tt.err)
				return
			}

			require.False(diags.HasErrors(), diags.Error())
			for k, v := range tt.expected {
				diff := cmp.Diff(v, ivs[k], cmpOpts...)
				if diff != "" {
					t.Fatalf("Expected variables differed from actual: %s", diff)
				}
			}
		})
	}
}

func TestVariables_SetJobInputVariables(t *testing.T) {
	cases := []struct {
		name     string
		files    []string
		cliArgs  map[string]string
		expected []*pb.Variable
		err      string
	}{
		{
			"cli args",
			[]string{""},
			map[string]string{"foo": "bar"},
			[]*pb.Variable{
				{
					Name:   "foo",
					Value:  &pb.Variable_Str{Str: "bar"},
					Source: &pb.Variable_Cli{},
				},
			},
			"",
		},
		{
			"files",
			[]string{filepath.Join("testdata", "values.wpvars"), filepath.Join("testdata", "more_values.wpvars")},
			nil,
			[]*pb.Variable{
				{
					Name:   "mug",
					Value:  &pb.Variable_Str{Str: "yeti"},
					Source: &pb.Variable_File_{},
				},
				{
					Name:   "art",
					Value:  &pb.Variable_Str{Str: "gdbee"},
					Source: &pb.Variable_File_{},
				},
				{
					Name:   "is_good",
					Value:  &pb.Variable_Bool{Bool: true},
					Source: &pb.Variable_File_{},
				},
				{
					Name:   "whatdoesittaketobenumber",
					Value:  &pb.Variable_Num{Num: 1},
					Source: &pb.Variable_File_{},
				},
			},
			"",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			vars, diags := LoadVariableValues(tt.cliArgs, tt.files)
			require.False(diags.HasErrors())

			require.Equal(len(tt.expected), len(vars))
			for _, v := range tt.expected {
				require.Contains(vars, v)
			}
		})
	}
}

func TestLoadEnvValues(t *testing.T) {
	cases := []struct {
		name     string
		vars     map[string]*Variable
		env      map[string]string
		expected map[string]string
	}{
		{
			"WP_VAR_ always wins",
			map[string]*Variable{
				"foo": {
					Name: "foo",
					Env:  []string{"one", "two"},
				},
			},
			map[string]string{"WP_VAR_foo": "x", "one": "1", "two": "2"},
			map[string]string{"foo": "x"},
		},

		{
			"first match takes priority",
			map[string]*Variable{
				"foo": {
					Name: "foo",
					Env:  []string{"one", "two"},
				},
			},
			map[string]string{"one": "1", "two": "2"},
			map[string]string{"foo": "1"},
		},

		{
			"first match takes priority (second set)",
			map[string]*Variable{
				"foo": {
					Name: "foo",
					Env:  []string{"one", "two"},
				},
			},
			map[string]string{"two": "2"},
			map[string]string{"foo": "2"},
		},

		{
			"none set",
			map[string]*Variable{
				"foo": {
					Name: "foo",
					Env:  []string{"one", "two"},
				},
			},
			map[string]string{},
			map[string]string{},
		},

		{
			"env key not set",
			map[string]*Variable{
				"foo": {
					Name: "foo",
				},
			},
			map[string]string{"one": "1", "two": "2"},
			map[string]string{},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			// Set our env vars
			for k, v := range tt.env {
				defer os.Setenv(k, os.Getenv(k))
				require.NoError(os.Setenv(k, v))
			}

			actual, diags := LoadEnvValues(tt.vars)
			require.False(diags.HasErrors(), diags.Error())

			actualMap := map[string]string{}
			for _, v := range actual {
				actualMap[v.Name] = v.Value.(*pb.Variable_Str).Str
			}

			require.Equal(tt.expected, actualMap)
		})
	}
}

// helper functions
var ctyValueComparer = cmp.Comparer(func(x, y cty.Value) bool {
	return x.RawEquals(y)
})

var ctyTypeComparer = cmp.Comparer(func(x, y cty.Type) bool {
	if x == cty.NilType && y == cty.NilType {
		return true
	}
	if x == cty.NilType || y == cty.NilType {
		return false
	}
	return x.Equals(y)
})

var cmpOpts = []cmp.Option{
	ctyValueComparer,
	ctyTypeComparer,
	cmpopts.IgnoreInterfaces(struct{ hcl.Expression }{}),
	cmpopts.IgnoreTypes(hclsyntax.TemplateExpr{}),
	cmpopts.IgnoreTypes(hcl.Range{}),
}

type testConfig struct {
	Variables []*HclVariable `hcl:"variable,block"`
	Body      hcl.Body       `hcl:",body"`
}

func stringListVal(strings ...string) cty.Value {
	values := []cty.Value{}
	for _, str := range strings {
		values = append(values, cty.StringVal(str))
	}
	list, err := convert.Convert(cty.ListVal(values), cty.List(cty.String))
	if err != nil {
		panic(err)
	}
	return list
}
