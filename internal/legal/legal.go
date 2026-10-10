// Package legal retains the notices for runtime code included in exports.
package legal

import "embed"

//go:embed LICENSE NOTICE GO_LICENSE
var notices embed.FS

func Text(name string) []byte { data, _ := notices.ReadFile(name); return data }
func HTMLNotice() string {
	return "\n<!-- Playfielder runtime license and notices (not a license for the author's original story or assets):\n" + string(Text("NOTICE")) + "\n" + string(Text("LICENSE")) + "\nGo runtime:\n" + string(Text("GO_LICENSE")) + "\n-->\n"
}
