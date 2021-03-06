package apigee

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/zambien/go-apigee-edge"
)

func resourceApiProxyDeployment() *schema.Resource {
	return &schema.Resource{
		Create: resourceApiProxyDeploymentCreate,
		Read:   resourceApiProxyDeploymentRead,
		Update: resourceApiProxyDeploymentUpdate,
		Delete: resourceApiProxyDeploymentDelete,
		Importer: &schema.ResourceImporter{
			State: resourceApiProxyDeploymentImport,
		},

		Schema: map[string]*schema.Schema{
			"proxy_name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"org": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"env": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"revision": {
				Type:     schema.TypeString,
				Required: true,
			},
			"delay": {
				Type:     schema.TypeInt,
				Optional: true,
				Default:  0,
			},
			"override": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func resourceApiProxyDeploymentImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	log.Print("[DEBUG] resourceApiProxyDeploymentImport START")
	client := meta.(*apigee.EdgeClient)

	if len(strings.Split(d.Id(), "_")) != 3 {
		return []*schema.ResourceData{}, fmt.Errorf("[ERR] Wrong format of resource: %s. Please follow '{name}_{env}_deployment'", d.Id())
	}
	name := strings.Split(d.Id(), "_")[0]
	IDEnv := strings.Split(d.Id(), "_")[1]
	deployment, _, err := client.Proxies.GetDeployments(name)
	if err != nil {
		log.Printf("[DEBUG] resourceApiProxyDeploymentImport. Error getting deployment api: %v", err)
		return nil, nil
	}
	d.Set("org", deployment.Organization)
	d.Set("proxy_name", deployment.Name)
	d.Set("env", IDEnv)

	return []*schema.ResourceData{d}, nil
}

func resourceApiProxyDeploymentRead(d *schema.ResourceData, meta interface{}) (e error) {
	log.Print("[DEBUG] resourceApiProxyDeploymentRead START")
	log.Printf("[DEBUG] resourceApiProxyDeploymentRead proxy_name: %#v", d.Get("proxy_name").(string))

	client := meta.(*apigee.EdgeClient)

	found := false
	latestRevision := "0"

	if deployments, _, err := client.Proxies.GetDeployments(d.Get("proxy_name").(string)); err != nil {
		log.Printf("[ERROR] resourceApiProxyDeploymentRead error getting deployments: %s", err.Error())
		if strings.Contains(err.Error(), "404 ") {
			log.Printf("[DEBUG] resourceApiProxyDeploymentRead 404 encountered.  Removing state for deployment proxy_name: %#v", d.Get("proxy_name").(string))
			d.SetId("")
			return nil
		} else {
			return fmt.Errorf("[ERROR] resourceApiProxyDeploymentRead error reading deployments: %s", err.Error())
		}
	} else {
		log.Printf("[DEBUG] resourceApiProxyDeploymentRead deployments call fired for proxy_name: %#v", d.Get("proxy_name").(string))
		for _, environment := range deployments.Environments {
			log.Printf("[DEBUG] resourceApiProxyDeploymentRead checking revisions in deployed environment: %#v for expected environment: %#v\n", environment.Name, d.Get("env").(string))
			if environment.Name == d.Get("env").(string) {
				//We don't break.  Always get the last one if there are multiple deployments.
				for _, revision := range environment.Revision {
					log.Printf("[DEBUG] resourceApiProxyDeploymentRead checking deployed revision: %#v for expected revision: %#v\n", revision.Number.String(), d.Get("revision").(string))
					latestRevision = revision.Number.String()
					found = true
				}
			}
		}
	}

	if found {
		log.Printf("[DEBUG] resourceApiProxyDeploymentRead - deployment found. Revision is: %#v", latestRevision)
		d.Set("revision", latestRevision)
	} else {
		log.Print("[DEBUG] resourceApiProxyDeploymentRead - no deployment found")
		d.SetId("")
	}
	return nil
}

func resourceApiProxyDeploymentCreate(d *schema.ResourceData, meta interface{}) error {

	log.Print("[DEBUG] resourceApiProxyDeploymentCreate START")

	client := meta.(*apigee.EdgeClient)

	proxy_name := d.Get("proxy_name").(string)
	env := d.Get("env").(string)
	rev_int, _ := strconv.Atoi(d.Get("revision").(string))
	rev := apigee.Revision(rev_int)
	delay := int(d.Get("delay").(int))
	override := bool(d.Get("override").(bool))

	if d.Get("revision").(string) == "latest" {
		// deploy latest
		rev, err := getLatestRevision(client, proxy_name)
		if err != nil {
			return fmt.Errorf("[ERROR] resourceApiProxyDeploymentUpdate error getting latest revision: %v", err)
		}
		_, _, err = client.Proxies.Deploy(proxy_name, env, apigee.Revision(rev), delay, override)
		if err != nil {
			return fmt.Errorf("[ERROR] resourceApiProxyDeploymentUpdate error deploying: %v", err)
		}
		log.Printf("[DEBUG] resourceApiProxyDeploymentUpdate Deployed revision %d of %s", rev, proxy_name)
		return resourceApiProxyDeploymentRead(d, meta)
	}

	proxyDep, _, err := client.Proxies.Deploy(proxy_name, env, rev, delay, override)

	if err != nil {

		if strings.Contains(err.Error(), "conflicts with existing deployment path") {
			//create, fail, update
			log.Printf("[ERROR] resourceApiProxyDeploymentCreate error deploying: %s", err.Error())
			log.Print("[DEBUG] resourceApiProxyDeploymentCreate something got out of sync... maybe someone messing around in apigee directly.  Terraform OVERRIDE!!!")
			resourceApiProxyDeploymentUpdate(d, meta)
		} else {
			log.Printf("[ERROR] resourceApiProxyDeploymentCreate error deploying: %s", err.Error())
			return fmt.Errorf("[ERROR] resourceApiProxyDeploymentCreate error deploying: %s", err.Error())
		}
	}

	id, _ := uuid.NewV4()
	d.SetId(id.String())
	d.Set("revision", proxyDep.Revision.String())

	return resourceApiProxyDeploymentRead(d, meta)
}

func resourceApiProxyDeploymentUpdate(d *schema.ResourceData, meta interface{}) error {

	log.Print("[DEBUG] resourceApiProxyDeploymentUpdate START")

	client := meta.(*apigee.EdgeClient)

	proxy_name := d.Get("proxy_name").(string)
	env := d.Get("env").(string)
	delay := int(d.Get("delay").(int))
	override := bool(d.Get("override").(bool))

	//We must set delay and override here if not set.
	if delay == 0 {
		delay = 15 //seconds
	}
	if override == false {
		override = true
	}

	if d.Get("revision").(string) == "latest" {
		// deploy latest
		rev, err := getLatestRevision(client, proxy_name)
		if err != nil {
			return fmt.Errorf("[ERROR] resourceApiProxyDeploymentUpdate error getting latest revision: %v", err)
		}
		_, _, err = client.Proxies.ReDeploy(proxy_name, env, apigee.Revision(rev), delay, override)
		if err != nil {
			return fmt.Errorf("[ERROR] resourceApiProxyDeploymentUpdate error deploying: %v", err)
		}
		log.Printf("[DEBUG] resourceApiProxyDeploymentUpdate Deployed revision %d of %s", rev, proxy_name)
		return resourceApiProxyDeploymentRead(d, meta)
	}

	rev_int, _ := strconv.Atoi(d.Get("revision").(string))
	rev := apigee.Revision(rev_int)
	_, _, err := client.Proxies.ReDeploy(proxy_name, env, rev, delay, override)

	if err != nil {
		log.Printf("[ERROR] resourceApiProxyDeploymentUpdate error redeploying: %s", err.Error())
		if strings.Contains(err.Error(), " is already deployed into environment ") {
			return resourceApiProxyDeploymentRead(d, meta)
		}
		return fmt.Errorf("[ERROR] resourceApiProxyDeploymentUpdate error redeploying: %s", err.Error())
	}

	return resourceApiProxyDeploymentRead(d, meta)
}

func resourceApiProxyDeploymentDelete(d *schema.ResourceData, meta interface{}) error {

	log.Print("[DEBUG] resourceApiProxyDeploymentDelete START")

	client := meta.(*apigee.EdgeClient)

	proxy_name := d.Get("proxy_name").(string)
	env := d.Get("env").(string)
	rev_int, _ := strconv.Atoi(d.Get("revision").(string))
	rev := apigee.Revision(rev_int)

	_, _, err := client.Proxies.Undeploy(proxy_name, env, rev)
	if err != nil {
		log.Printf("[ERROR] resourceApiProxyDeploymentDelete error undeploying: %s", err.Error())
		return fmt.Errorf("[ERROR] resourceApiProxyDeploymentDelete error undeploying: %s", err.Error())
	}

	return nil
}

func getLatestRevision(client *apigee.EdgeClient, proxyName string) (int, error) {
	proxy, _, err := client.Proxies.Get(proxyName)
	if err != nil {
		return -1, fmt.Errorf("[ERROR] resourceApiProxyRead error reading proxies: %s", err.Error())
	}
	return len(proxy.Revisions), nil
}
