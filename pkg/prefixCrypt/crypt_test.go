package prefixCrypt

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

func TestCrypt(t *testing.T) {
	var data = []byte(`Lorem ipsum dolor sit amet, consectetur adipiscing elit. Aliquam vel consectetur odio. Fusce finibus rutrum lectus, quis accumsan urna luctus ac. Proin interdum et metus sed imperdiet. Mauris eget viverra nulla. Sed tristique nunc at pharetra dictum. Aenean suscipit mattis faucibus. Donec ornare condimentum scelerisque. Orci varius natoque penatibus et magnis dis parturient montes, nascetur ridiculus mus.
		Sed ac faucibus erat, non posuere nunc. Aliquam quis sollicitudin neque. Nunc euismod ut neque in varius. Maecenas pulvinar, tellus non rutrum elementum, metus ipsum cursus sapien, fringilla convallis felis ex eget ligula. Sed sapien augue, facilisis vel efficitur non, posuere vitae arcu. In feugiat, tortor in viverra posuere, augue ligula sodales nibh, non maximus ligula ligula non lacus. Duis convallis semper velit, a laoreet purus sagittis in. Donec eu lectus libero. Sed sollicitudin bibendum ante, nec pharetra odio sodales sed. Suspendisse tristique eros a purus fringilla, eget placerat massa feugiat. Etiam tempus arcu ac turpis gravida facilisis.
		Lorem ipsum dolor sit amet, consectetur adipiscing elit. Duis interdum non nulla vitae vestibulum. In eu diam dictum, semper lorem non, elementum ligula. Vestibulum lectus orci, cursus laoreet facilisis et, suscipit eget est. Aenean et suscipit dolor. Cras condimentum dolor eu libero placerat feugiat. Nulla vitae lorem malesuada, cursus lacus a, mollis diam. Morbi feugiat nisi id interdum suscipit. Donec ac lectus aliquet, sagittis ante sed, varius diam. Donec semper consectetur posuere. Sed dapibus commodo orci ac mattis. Duis nec dui fermentum, finibus ipsum a, faucibus quam.
		Nam porttitor nunc eros, quis convallis magna fermentum quis. Maecenas vel commodo eros. Aenean vel sapien sed lorem lacinia tristique ultrices eget risus. Vivamus sit amet leo quis magna fermentum pulvinar. Praesent convallis elit lectus, rhoncus convallis turpis pulvinar vel. Maecenas tincidunt, eros vel aliquam suscipit, lacus magna convallis turpis, at finibus sapien purus ac enim. Vestibulum non mauris pretium, auctor sem sed, porttitor erat. Etiam vel odio mi.
		Phasellus tincidunt ultricies gravida. Quisque eget leo sem. Integer egestas malesuada ipsum eget ornare. Curabitur interdum elit vel nisi vulputate egestas. Praesent eu viverra turpis. Nam augue nibh, hendrerit eu ornare a, congue auctor sapien. Aenean ut dui at lorem sagittis tincidunt. Duis auctor porttitor fermentum. In auctor ante sem, vitae placerat lectus tincidunt id. Nulla in accumsan justo, ut ultricies mauris.`)

	fp := bytes.NewBuffer(nil)
	wc := NewEncWriter(fp, Encrypt)
	if _, err := io.Copy(wc, bytes.NewBuffer(data)); err != nil {
		t.Fatalf("cannot write file: %v", err)

		return
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("cannot close writer: %v", err)
		return
	}
	encData := fp.Bytes()

	encFP := bytes.NewReader(encData)
	rc, err := NewDecryptReader(encFP, Decrypt)
	if err != nil {
		t.Fatalf("cannot create reader: %v", err)
		return
	}
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, rc); err != nil {
		t.Fatalf("cannot read file: %v", err)
		return
	}

	if !bytes.Equal(data, buf.Bytes()) {
		t.Fatalf("data not equal")
	}

	if _, err := rc.Seek(6, io.SeekStart); err != nil {
		t.Fatalf("cannot seek: %v", err)
		return
	}

	buf = new(bytes.Buffer)
	if _, err := io.Copy(buf, rc); err != nil {
		t.Fatalf("cannot read file: %v", err)
		return
	}

	if !bytes.Equal(data[6:], buf.Bytes()) {
		t.Fatalf("seek data not equal")
	}

	fmt.Printf("decrypted: %s\n", buf.String())
	return

}
