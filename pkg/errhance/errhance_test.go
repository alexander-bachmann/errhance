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

func TestDo(t *testing.T) {
	type args struct {
		config Config
		src    string
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
			},
			want: `package main
				import (
					"fee/fi/fo/fum"
				)
				func main() {
					var foo Foo
					val, err := foo.Foo()
					if err != nil {
						return val, fmt.Errorf("Foo: %w", err)
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
			},
			want: main(`
				var foo Foo
				val, err := foo.Foo()
				if err != nil {
					return val, fmt.Errorf("Foo: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "chaos",
			args: args{
				src: main(`
				val := foo.Foo()
				if err != nil {
					return err
				}`),
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
			},
			want: main(`
				err := foo.Foo()
				if err != nil {
					return fmt.Errorf("Foo: %w", err)
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
			},
			want: main(`
				err := bar(Foo())
				if err != nil {
					return fmt.Errorf("bar: %w", err)
				}`),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Do(tt.args.config, tt.args.src)
			if (err != nil) != tt.wantErr {
				t.Errorf("Do() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Do() = %v, want %v", got, tt.want)
			}
		})
	}
}
