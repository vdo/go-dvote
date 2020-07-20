package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gitlab.com/vocdoni/go-dvote/log"
	"gitlab.com/vocdoni/go-dvote/types"
)

const fetchFileTimeout = time.Second * 10

func (r *Router) fetchFile(request routerRequest) {
	log.Debugf("calling FetchFile %s", request.URI)
	parsedURIs := strings.Split(request.URI, ",")
	transportTypes := parseTransportFromURI(parsedURIs)
	var content []byte
	var err error

	found := false
	for idx, t := range transportTypes {
		switch t {
		case "ipfs:":
			found = true
			splt := strings.Split(parsedURIs[idx], "/")
			hash := splt[len(splt)-1]
			ctx, cancel := context.WithTimeout(context.Background(), fetchFileTimeout)
			content, err = r.storage.Retrieve(ctx, hash)
			if err == nil && len(content) == 0 {
				err = fmt.Errorf("no content fetched")
			}
			cancel()
		case "bzz:", "bzz-feed":
			found = true
			err = fmt.Errorf("bzz and bzz-feed not implemented yet")
		}
		if found {
			break
		}
	}

	if err != nil {
		r.sendError(request, fmt.Sprintf("error fetching file: (%s)", err))
		return
	}
	if !found {
		r.sendError(request, "error fetching file: (not supported)")
		return
	}

	b64content := base64.StdEncoding.EncodeToString(content)
	log.Debugf("file fetched, b64 size %d", len(b64content))
	var response types.MetaResponse
	response.Content = b64content
	r.transport.Send(r.buildReply(request, &response))
}

func (r *Router) addFile(request routerRequest) {
	log.Debugf("calling addFile")
	reqType := request.Type
	b64content, err := base64.StdEncoding.DecodeString(request.Content)
	if err != nil {
		r.sendError(request, "could not decode base64 content")
		return
	}
	switch reqType {
	case "swarm":
		// TODO
	case "ipfs":
		cid, err := r.storage.Publish(context.TODO(), b64content)
		if err != nil {
			r.sendError(request,
				fmt.Sprintf("cannot add file (%s)", err))
			return
		}
		log.Debugf("added file %s, b64 size of %d", cid, len(b64content))
		var response types.MetaResponse
		response.URI = r.storage.URIprefix() + cid
		r.transport.Send(r.buildReply(request, &response))
	}
}

func (r *Router) pinList(request routerRequest) {
	log.Debug("calling PinList")
	pins, err := r.storage.ListPins(context.TODO())
	if err != nil {
		r.sendError(request, fmt.Sprintf("internal error fetching pins (%s)", err))
		return
	}
	pinsJSONArray, err := json.Marshal(pins)
	if err != nil {
		r.sendError(request, fmt.Sprintf("internal error parsing pins (%s)", err))
		return
	}
	var response types.MetaResponse
	response.Files = pinsJSONArray
	r.transport.Send(r.buildReply(request, &response))
}

func (r *Router) pinFile(request routerRequest) {
	log.Debugf("calling PinFile %s", request.URI)
	err := r.storage.Pin(context.TODO(), request.URI)
	if err != nil {
		r.sendError(request, fmt.Sprintf("error pinning file (%s)", err))
		return
	}
	var response types.MetaResponse
	r.transport.Send(r.buildReply(request, &response))
}

func (r *Router) unpinFile(request routerRequest) {
	log.Debugf("calling UnPinFile %s", request.URI)
	err := r.storage.Unpin(context.TODO(), request.URI)
	if err != nil {
		r.sendError(request, fmt.Sprintf("could not unpin file (%s)", err))
		return
	}
	var response types.MetaResponse
	r.transport.Send(r.buildReply(request, &response))
}
