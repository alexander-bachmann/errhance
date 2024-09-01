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
				val, lav, err := justDoIt()
				if err != nil {
					return val, lav, err
				}`),
			},
			want: main(`
				val, lav, err := justDoIt()
				if err != nil {
					return val, lav, fmt.Errorf("justDoIt: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "func returns 2, err != nil returns 2",
			args: args{
				src: main(`
				val, err := justDoIt()
				if err != nil {
					return val, err
				}`),
			},
			want: main(`
				val, err := justDoIt()
				if err != nil {
					return val, fmt.Errorf("justDoIt: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "method",
			args: args{
				src: main(`
				val, err := shia.justDoIt()
				if err != nil {
					return val, err
				}`),
			},
			want: main(`
				val, err := shia.justDoIt()
				if err != nil {
					return val, fmt.Errorf("shia.justDoIt: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "chaos",
			args: args{
				src: main(`
				val := shia.justDoIt()
				if err != nil {
					return err
				}`),
			},
			want: main(`
				val := shia.justDoIt()
				if err != nil {
					return err
				}`),
			wantErr: false,
		},
		{
			name: "func returns 2, err != nil returns 1",
			args: args{
				src: main(`
				val, err := justDoIt()
				if err != nil {
					return err
				}`),
			},
			want: main(`
				val, err := justDoIt()
				if err != nil {
					return fmt.Errorf("justDoIt: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "with comments",
			args: args{
				src: main(`
				val, err := justDoIt() // nice...
				// is this nil?
				if err != nil {
					// return that thang
					return err // return it here
				}`),
			},
			want: main(`
				val, err := justDoIt() // nice...
				// is this nil?
				if err != nil {
					// return that thang
					return fmt.Errorf("justDoIt: %w", err) // return it here
				}`),
			wantErr: false,
		},
		{
			name: "multiple",
			args: args{
				src: main(`
				err := shia.justDoIt()
				if err != nil {
					return err
				}
				err = dontLetYourDreamsBeDreams()
				if err != nil {
					return err
				}`),
			},
			want: main(`
				err := shia.justDoIt()
				if err != nil {
					return fmt.Errorf("shia.justDoIt: %w", err)
				}
				err = dontLetYourDreamsBeDreams()
				if err != nil {
					return fmt.Errorf("dontLetYourDreamsBeDreams: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "func returns 1, err != nil returns 1",
			args: args{
				src: main(`
				err := dontLetYourDreamsBeDreams()
				if err != nil {
					return err
				}`),
			},
			want: main(`
				err := dontLetYourDreamsBeDreams()
				if err != nil {
					return fmt.Errorf("dontLetYourDreamsBeDreams: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "nested func calls",
			args: args{
				src: main(`
				err := dontLetYourDreamsBeDreams(justDoIt())
				if err != nil {
					return err
				}`),
			},
			want: main(`
				err := dontLetYourDreamsBeDreams(justDoIt())
				if err != nil {
					return fmt.Errorf("dontLetYourDreamsBeDreams: %w", err)
				}`),
			wantErr: false,
		},
		{
			name: "nested func calls",
			args: args{
				src: main(`
				err := dontLetYourDreamsBeDreams(justDoIt())
				if err != nil {
					return err
				}`),
			},
			want: main(`
				err := dontLetYourDreamsBeDreams(justDoIt())
				if err != nil {
					return fmt.Errorf("dontLetYourDreamsBeDreams: %w", err)
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
