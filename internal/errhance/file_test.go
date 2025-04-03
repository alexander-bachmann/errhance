package errhance

import (
	"errors"
	"fmt"
	"testing"
)

func main(replacement string) string {
	return fmt.Sprintf(`package main
func main() {
	%s
}`, replacement)
}

func TestSource(t *testing.T) {
	type args struct {
		src    string
		config SourceConfig
	}
	tests := []struct {
		name    string
		args    args
		after   string
		wantErr bool
	}{
		{
			name: "3 return values with err enhancement",
			args: args{
				src: main(`
				val, lav, err := Foo()
				if err != nil {
					return val, lav, err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				val, lav, err := Foo()
				if err != nil {
					return val, lav, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "2 return values with enhancement",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "enhancing method func and package func",
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
				config: NewSourceConfig(),
			},
			after: `package main
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
			name: "method enhanced",
			args: args{
				src: main(`
				var foo Foo
				val, err := foo.Foo()
				if err != nil {
					return val, err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				var foo Foo
				val, err := foo.Foo()
				if err != nil {
					return val, fmt.Errorf("foo.Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "returned error is not named err (would be a bug in original src)",
			args: args{
				src: main(`
				val := foo.Foo()
				if err != nil {
					return err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				val := foo.Foo()
				if err != nil {
					return err
				}`),
			wantErr: false,
		},
		{
			name: "calling func returns 2 values, parent func returns 1 value",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
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
				config: NewSourceConfig(),
			},
			after: main(`
				val, err := Foo() // nice...
				// is this nil?
				if err != nil {
					// return that thang
					return fmt.Errorf("Foo: %w", err) // return it here
				}`),
			wantErr: false,
		},
		{
			name: "multiple enhancements in a single file",
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
				config: NewSourceConfig(),
			},
			after: main(`
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
			name: "calling func returns 1 value, parent func returns 1 value",
			args: args{
				src: main(`
				err := bar()
				if err != nil {
					return err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
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
				config: NewSourceConfig(),
			},
			after: main(`
				err := bar(Foo())
				if err != nil {
					return fmt.Errorf("bar: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "most variants in a single file",
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
				config: NewSourceConfig(),
			},
			after: `package main
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
				config: NewSourceConfig(),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, err //noeh
				}`),
			wantErr: false,
		},
		{
			name: "no --overwrite flag provided with pre-existing fmt.Errorf",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed to run Foo: %w", err)
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
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
				config: NewSourceConfig(WithOverwrite(true)),
			},
			after: main(`
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
				config: NewSourceConfig(),
			},
			after: main(`
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
				config: NewSourceConfig(WithGenerated(true)),
			},
			after: main(`
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
				config: NewSourceConfig(),
			},
			after: main(`
				val, err := Foo().Bar(Baz()).Flop()
				if err != nil {
					return val, fmt.Errorf("Foo.Bar.Flop: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "default, no change to fmt.Errorf with multiple idents",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed: %s: %w", val, err)
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed: %s: %w", val, err)
				}`),
			wantErr: false,
		},
		{
			name: "with --overwrite, no change to fmt.Errorf with multiple idents",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, 0, 0, fmt.Errorf("failed: %s: %w", val, err)
				}`),
				config: NewSourceConfig(WithOverwrite(true)),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, 0, 0, fmt.Errorf("failed: %s: %w", val, err)
				}`),
			wantErr: false,
		},
		{
			name: "with --force-overwrite, change to fmt.Errorf with multiple idents",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, 0, 0, fmt.Errorf("failed: %s: %w", val, err)
				}`),
				config: NewSourceConfig(WithForceOverwrite(true)),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, 0, 0, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "default, no change to fmt.Errorf with sentinel err",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed: %w", ErrNotFound)
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed: %w", ErrNotFound)
				}`),
			wantErr: false,
		},
		{
			name: "with --overwrite, no change to fmt.Errorf with sentinel err",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, 0, fmt.Errorf("failed: %w", ErrNotFound)
				}`),
				config: NewSourceConfig(WithOverwrite(true)),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, 0, fmt.Errorf("failed: %w", ErrNotFound)
				}`),
			wantErr: false,
		},
		{
			name: "with --force-overwrite, change to fmt.Errorf with sentinel err",
			args: args{
				src: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("failed: %w", ErrNotFound)
				}`),
				config: NewSourceConfig(WithForceOverwrite(true)),
			},
			after: main(`
				val, err := Foo()
				if err != nil {
					return val, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "with --overwrite, no change and formatting args are not just a simple identifier expression",
			args: args{
				src: main(`
				val, err := strconv.Atoi(str[0])
				if err != nil {
					return 0, 0, 0, fmt.Errorf("%s: %w", str[0], err)
				}`),
				config: NewSourceConfig(WithOverwrite(true)),
			},
			after: main(`
				val, err := strconv.Atoi(str[0])
				if err != nil {
					return 0, 0, 0, fmt.Errorf("%s: %w", str[0], err)
				}`),
			wantErr: false,
		},
		{
			name: "error channel syntax unsupported (for now)",
			args: args{
				src: main(`
				if err = <-errCh; err != nil {
					return nil, err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				if err = <-errCh; err != nil {
					return nil, err
				}`),
			wantErr: false,
		},
		{
			name: "weird",
			args: args{
				src: main(`
				err = a().b().c().d()
				if err != nil {
					return nil, err
				}
				if foo := uint32(bar - baz); foo> 0 {
					if err := f.aoeu.snth.Read(1, foo); err != nil {
						err = something(true, err, "bleep", err)
						return nil, err
					}
				}
				val, lav, err := Foo()
				if err != nil {
					return nil, err
				}`),
				config: NewSourceConfig(),
			},
			after: main(`
				err = a().b().c().d()
				if err != nil {
					return nil, fmt.Errorf("a.b.c.d: %w", err)
				}
				if foo := uint32(bar - baz); foo> 0 {
					if err := f.aoeu.snth.Read(1, foo); err != nil {
						err = something(true, err, "bleep", err)
						return nil, err
					}
				}
				val, lav, err := Foo()
				if err != nil {
					return nil, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "one method shorthand followed by error channel shorthand",
			args: args{
				src: main(`
					if err := r.Flush(); err != nil {
						return nil, err
					}
					if err = <-errCh; err != nil {
						return nil, err
					}`),
				config: NewSourceConfig(),
			},
			after: main(`
					if err := r.Flush(); err != nil {
						return nil, fmt.Errorf("r.Flush: %w", err)
					}
					if err = <-errCh; err != nil {
						return nil, err
					}`),
			wantErr: false,
		},
		{
			name: "one shorthand syntax statement #1",
			args: args{
				src: main(`
		if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
			return nil, err
		}`),
				config: NewSourceConfig(),
			},
			after: main(`
		if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
			return nil, fmt.Errorf("c.Request.ParseMultipartForm: %w", err)
		}`),
			wantErr: false,
		},
		{
			name: "one shorthand syntax statement #2",
			args: args{
				src: main(`
		if err := rows.Err(); err != nil {
			return nil, err
		}`),
				config: NewSourceConfig(),
			},
			after: main(`
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows.Err: %w", err)
		}`),
			wantErr: false,
		},
		{
			name: "double shorthand syntax",
			args: args{
				src: main(`
		if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}`),
				config: NewSourceConfig(),
			},
			after: main(`
		if err := c.Request().ParseMultipartForm(10 << 20); err != nil {
			return nil, fmt.Errorf("c.Request.ParseMultipartForm: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows.Err: %w", err)
		}`),
			wantErr: false,
		},
		{
			name: "nested shorthands",
			args: args{
				src: main(`
		if d := uint32(aoeu - snth); d > 0 {
			if err := a.abc.zxy.Write(0, d); err != nil {
				return nil, err
			}
		}`),
				config: NewSourceConfig(),
			},
			after: main(`
		if d := uint32(aoeu - snth); d > 0 {
			if err := a.abc.zxy.Write(0, d); err != nil {
				return nil, fmt.Errorf("a.abc.zxy.Write: %w", err)
			}
		}`),
			wantErr: false,
		},
		{
			name: "err is overwritten inside if, don't support",
			args: args{
				src: main(`
		if foo := uint32(bar - baz); foo> 0 {
			if err := f.aoeu.snth.Read(1, foo); err != nil {
				err = something(true, err, "bleep", err)
				return nil, err
			}
		}`),
				config: NewSourceConfig(),
			},
			after: main(`
		if foo := uint32(bar - baz); foo> 0 {
			if err := f.aoeu.snth.Read(1, foo); err != nil {
				err = something(true, err, "bleep", err)
				return nil, err
			}
		}`),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Source(tt.args.config, "", tt.args.src)
			if (err != nil) != tt.wantErr && !errors.Is(err, ErrNoChanges) {
				t.Errorf("Source() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if report.After != tt.after {
				t.Errorf("After: got = %v, want = %v", report.After, tt.after)
			}
		})
	}
}
