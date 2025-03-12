package errhance

import (
	"fmt"
	"testing"
)

func main(replacement string) string {
	return fmt.Sprintf(`package main
func main() {
	%s
}`, replacement)
}

func TestFile(t *testing.T) {
	type args struct {
		src    string
		config Config
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "func returns 3, err != nil returns 3",
			args: args{
				src: main(`
				val, lav, err := Foo()
				if err != nil {
					return val, lav, err
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, lav, err := Foo()
				if err != nil {
					return val, lav, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "func returns 2, err != nil returns 2",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, err
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "method and package",
			args: args{
				src: `package main
				import (
					"fee/fi/fo/fum"
				)
				func main() {
					var foo Foo
					val, err := foo.Foo()
					if err != nil {
						return val, err
					}
					v, err = fum.Foo()
					if err != nil {
						return val, err
					}
				}`,
				config: NewConfig(),
			},
			want: `package main
				import (
					"fee/fi/fo/fum"
				)
				func main() {
					var foo Foo
					val, err := foo.Foo()
					if err != nil {
						return val, fmt.Errorf("foo.Foo: %w", err)
					}
					v, err = fum.Foo()
					if err != nil {
						return val, fmt.Errorf("fum.Foo: %w", err)
					}
				}`,
			wantErr: false,
		},

		{
			name: "method",
			args: args{
				src: main(`
				var foo Foo
				val, err := foo.Foo()
				if err != nil {
					return val, err
				}`),
				config: NewConfig(),
			},
			want: main(`
				var foo Foo
				val, err := foo.Foo()
				if err != nil {
					return val, fmt.Errorf("foo.Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "returned error is not named err",
			args: args{
				src: main(`
				val := foo.Foo()
				if err != nil {
					return err
				}`),
				config: NewConfig(),
			},
			want: main(`
				val := foo.Foo()
				if err != nil {
					return err
				}`),
			wantErr: false,
		},
		{
			name: "func returns 2, err != nil returns 1",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return err
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, err := Foo()
				if err != nil {
					return fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "with comments",
			args: args{
				src: main(`
				val, err := Foo() // nice...
				// is this nil?
				if err != nil {
					// return that thang
					return err // return it here
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, err := Foo() // nice...
				// is this nil?
				if err != nil {
					// return that thang
					return fmt.Errorf("Foo: %w", err) // return it here
				}`),
			wantErr: false,
		},
		{
			name: "multiple",
			args: args{
				src: main(`
				err := foo.Foo()
				if err != nil {
					return err
				}
				err = bar()
				if err != nil {
					return err
				}`),
				config: NewConfig(),
			},
			want: main(`
				err := foo.Foo()
				if err != nil {
					return fmt.Errorf("foo.Foo: %w", err)
				}
				err = bar()
				if err != nil {
					return fmt.Errorf("bar: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "func returns 1, err != nil returns 1",
			args: args{
				src: main(`
				err := bar()
				if err != nil {
					return err
				}`),
				config: NewConfig(),
			},
			want: main(`
				err := bar()
				if err != nil {
					return fmt.Errorf("bar: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "nested func calls",
			args: args{
				src: main(`
				err := bar(Foo())
				if err != nil {
					return err
				}`),
				config: NewConfig(),
			},
			want: main(`
				err := bar(Foo())
				if err != nil {
					return fmt.Errorf("bar: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "nested func calls",
			args: args{
				src: main(`
				err := bar(Foo())
				if err != nil {
					return err
				}`),
				config: NewConfig(),
			},
			want: main(`
				err := bar(Foo())
				if err != nil {
					return fmt.Errorf("bar: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "all",
			args: args{
				src: `package main
		import (
		  "foo"
		  "fee/fi/fo"
		  fid "fiddly"
		)
		func A() error {
		  b := foo.Bar{}
		  err := b.Baz()
		  if err != nil {
		    return err
		  }
		  err = fo.Fum()
		  if err != nil {
		    return err
		  }
		  err = meep()
		  if err != nil {
		    return err
		  }
		  err = fid.Widdly().Weddly().Woddly()
		  if err != nil {
		    return err
		  }
		  err = a().b().c().d()
		  if err != nil {
		    return err
		  }
		}`,
				config: NewConfig(),
			},
			want: `package main
		import (
		  "foo"
		  "fee/fi/fo"
		  fid "fiddly"
		)
		func A() error {
		  b := foo.Bar{}
		  err := b.Baz()
		  if err != nil {
		    return fmt.Errorf("b.Baz: %w", err)
		  }
		  err = fo.Fum()
		  if err != nil {
		    return fmt.Errorf("fo.Fum: %w", err)
		  }
		  err = meep()
		  if err != nil {
		    return fmt.Errorf("meep: %w", err)
		  }
		  err = fid.Widdly().Weddly().Woddly()
		  if err != nil {
		    return fmt.Errorf("fid.Widdly.Weddly.Woddly: %w", err)
		  }
		  err = a().b().c().d()
		  if err != nil {
		    return fmt.Errorf("a.b.c.d: %w", err)
		  }
		}`,
			wantErr: false,
		},
		{
			name: "contains omit comment",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, err //noeh
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, err := Foo()
				if err != nil {
					return val, err //noeh
				}`),
			wantErr: false,
		},
		{
			name: "do not overwrite previous fmt.Errorf",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed to run Foo: %w", err)
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed to run Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "overwrite previous fmt.Errorf",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed to run Foo: %w", err)
				}`),
				config: NewConfig(WithOverwrite()),
			},
			want: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "ignore generated code",
			args: args{
				src: main(`
				// Code generated by sqlc. DO NOT EDIT.
				// versions:
				//   sqlc v1.27.0
				val, err := Foo()
				if err != nil {
					return val, err
				}`),
				config: NewConfig(),
			},
			want: main(`
				// Code generated by sqlc. DO NOT EDIT.
				// versions:
				//   sqlc v1.27.0
				val, err := Foo()
				if err != nil {
					return val, err
				}`),
			wantErr: false,
		},
		{
			name: "errhance generated code",
			args: args{
				src: main(`
				// Code generated by sqlc. DO NOT EDIT.
				// versions:
				//   sqlc v1.27.0
				val, err := Foo()
				if err != nil {
					return val, err
				}`),
				config: NewConfig(WithGeneratedCode()),
			},
			want: main(`
				// Code generated by sqlc. DO NOT EDIT.
				// versions:
				//   sqlc v1.27.0
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "chained with nested func calls",
			args: args{
				src: main(`
				val, err := Foo().Bar(Baz()).Flop()
				if err != nil {
					return val, err
				}`),
				config: NewConfig(),
			},
			want: main(`
				val, err := Foo().Bar(Baz()).Flop()
				if err != nil {
					return val, fmt.Errorf("Foo.Bar.Flop: %w", err)
				}`),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := File(tt.args.src, tt.args.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("File() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("File() = %v, want %v", got, tt.want)
			}
		})
	}
}
