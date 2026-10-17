package cli

import "testing"

func TestDesktopArgs(t *testing.T) {
	cases := []struct {
		name    string
		desktop bool
		in      []string
		want    []string
	}{
		{name: "非桌面无参数原样", desktop: false, in: nil, want: nil},
		{name: "非桌面带参数原样", desktop: false, in: []string{"ui", "--no-open"}, want: []string{"ui", "--no-open"}},
		{name: "非桌面 psn 参数也不特殊", desktop: false, in: []string{"-psn_0_123456"}, want: []string{"-psn_0_123456"}},
		{name: "桌面无参数开窗", desktop: true, in: nil, want: []string{"app"}},
		{name: "桌面空参数同样开窗", desktop: true, in: []string{}, want: []string{"app"}},
		{name: "桌面 psn 参数视同无参数", desktop: true, in: []string{"-psn_0_123456"}, want: []string{"app"}},
		{name: "桌面用户给了子命令照用户的", desktop: true, in: []string{"ui"}, want: []string{"ui"}},
		{name: "桌面用户给了 flag 照用户的", desktop: true, in: []string{"--version"}, want: []string{"--version"}},
		{name: "桌面 psn 带别的参数不摊派", desktop: true, in: []string{"-psn_0_1", "status"}, want: []string{"-psn_0_1", "status"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DesktopArgs(tc.desktop, tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("DesktopArgs(%v, %v) = %v,想要 %v", tc.desktop, tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("DesktopArgs(%v, %v) = %v,想要 %v", tc.desktop, tc.in, got, tc.want)
				}
			}
		})
	}
}
